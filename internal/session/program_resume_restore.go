package session

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// Program resume (OSC 7577), the restore. See program_resume.go for how a
// record is made.
//
// A restored pane whose saved record names a program gets the offer an agent
// conversation gets (agent_resume.go): the same Inbox item, the same ask,
// auto and off modes of daemon.resume_agents, and the same rule that nothing
// is typed into a pane whose shell is not at its prompt. A pane with an agent
// conversation to offer keeps that offer, so one restart never starts the
// same agent twice. The offer is made once: the restore takes the record off
// the pane, and the program reports again when it runs.
//
// No shell text is ever built from what a program reported. The line typed
// into the restored shell is fixed: the absolute path of this tuios binary,
// the hidden resume-program command, and the path of a one-use file, mode
// 0600, that holds the argv and the folder as JSON. resume-program reads the
// file, removes it, looks argv[0] up on the shell's own PATH, changes to the
// folder and execs the program, so the program runs as the shell's
// foreground job, after the shell's startup files. A line whose paths hold a
// character outside a small set is not typed at all, so no shell has to
// quote it. The line starts with a space, which keeps it out of the history
// of bash with ignorespace, of zsh with hist_ignore_space, and of fish.

// resumeProgramCommand is the hidden command the typed line runs.
const resumeProgramCommand = "resume-program"

// errResumeUnsafePath says the line would need quoting, so it is not typed.
var errResumeUnsafePath = errors.New("the path of tuios or of the resume file needs quoting, so nothing was typed")

// programOffers holds the program records a restore offered, by window id,
// until one is typed. resume-agent and the Inbox find them here.
type programOffers struct {
	mu sync.Mutex
	m  map[string]*SavedProgram
}

func (o *programOffers) put(window string, sp *SavedProgram) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.m == nil {
		o.m = make(map[string]*SavedProgram)
	}
	o.m[window] = sp
}

func (o *programOffers) get(window string) *SavedProgram {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.m[window]
}

// take removes the window's offer and reports whether it was still there,
// so two answers to one offer type it once.
func (o *programOffers) take(window string, sp *SavedProgram) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.m[window] != sp {
		return false
	}
	delete(o.m, window)
	return true
}

// programOfferFor builds the offer for a window as it was saved, or reports
// that it has nothing to resume: the setting is off, no record, a record of
// another user, or a cwd that is gone.
func (d *Daemon) programOfferFor(sessionName string, w WindowState) (resumeOffer, bool) {
	sp := w.ProgramResume
	if sp == nil || len(sp.Argv) == 0 || !d.manager.ProgramResume() {
		return resumeOffer{}, false
	}
	if sp.UID != os.Getuid() {
		log.Printf("Not resuming the program of window %s: it was reported by another user", shortID(w.ID))
		return resumeOffer{}, false
	}
	if sp.Cwd != "" {
		if info, err := os.Stat(sp.Cwd); err != nil || !info.IsDir() {
			log.Printf("Not resuming the program of window %s: the folder it named is gone", shortID(w.ID))
			return resumeOffer{}, false
		}
	}
	d.programOffers.put(w.ID, sp)
	return resumeOffer{
		session:   sessionName,
		window:    w.ID,
		workspace: w.Workspace,
		name:      windowLabelOf(w),
		argv:      sp.Argv,
		program:   sp,
	}, true
}

// typeProgramResume types the line of a program offer into its restored
// shell, once.
func (d *Daemon) typeProgramResume(sess *Session, pty *PTY, plan resumePlan) error {
	if !d.programOffers.take(plan.window.ID, plan.program) {
		return errResumeNothing
	}
	// The restore took the record off the pane, and the state file says so
	// before anything runs: a resume that a crash follows is not run again
	// on the next start.
	if err := sess.persist(sess.ResurrectionState()); err != nil {
		LogError("Saving session %q before a program resume failed: %v", sess.Name(), err)
	}
	line, remove, err := d.programResumeLine(sess, plan.program)
	if err != nil {
		return err
	}
	if _, err := pty.Write([]byte(line + "\r")); err != nil {
		remove()
		return err
	}
	return nil
}

// clearProgramResumeFiles removes the one-use files an earlier daemon left.
// The shells it typed their lines into are gone.
func clearProgramResumeFiles() {
	dir := programResumeDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "program-") {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// programResumeDir holds the one-use files.
func programResumeDir() string {
	return filepath.Join(filepath.Dir(getResurrectionDir()), "resume")
}

// programResumeFile is what resume-program reads.
type programResumeFile struct {
	Argv []string `json:"argv"`
	Cwd  string   `json:"cwd,omitempty"`
	Dir  string   `json:"dir,omitempty"`
}

// programResumeLine writes the one-use file for sp and returns the line to
// type and what removes the file again, when the line is not typed.
func (d *Daemon) programResumeLine(sess *Session, sp *SavedProgram) (string, func(), error) {
	bin, err := os.Executable()
	if err != nil {
		return "", nil, err
	}
	dir := programResumeDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", nil, err
	}
	data, err := jsonx.Marshal(programResumeFile{Argv: sp.Argv, Cwd: sp.Cwd, Dir: sp.Dir})
	if err != nil {
		return "", nil, err
	}
	f, err := os.CreateTemp(dir, "program-*.json")
	if err != nil {
		return "", nil, err
	}
	path := f.Name()
	remove := func() { _ = os.Remove(path) }
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		remove()
		return "", nil, errors.Join(werr, cerr)
	}
	if !plainPath(bin) || !plainPath(path) {
		remove()
		return "", nil, errResumeUnsafePath
	}
	shell, _ := sess.resolveShell()
	return " " + shellCommandPrefix(shell) + bin + " " + resumeProgramCommand + " " + path, remove, nil
}

// shellCommandPrefix is what makes a shell run the program at a path, and
// not a function or an alias of that name. bash, zsh and the POSIX shells
// take \command, which no alias replaces; fish takes command; nushell runs
// an external program with ^. Any other shell gets the bare path.
func shellCommandPrefix(shell string) string {
	switch strings.TrimSuffix(filepath.Base(shell), ".exe") {
	case "bash", "zsh", "sh", "dash", "ksh", "mksh", "yash", "ash", "busybox":
		return "\\command "
	case "fish":
		return "command "
	case "nu":
		return "^"
	}
	return ""
}

// plainPath reports whether p is an absolute path that every shell reads as
// one word as written.
func plainPath(p string) bool {
	if !filepath.IsAbs(p) {
		return false
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		if !isAlnum(c) && !strings.ContainsRune("_./+-", rune(c)) {
			return false
		}
	}
	return true
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// ProgramCommandText is a reported command as the Inbox, list-attention and
// resume-agent show it: each argument as written when it is plain, and
// quoted with every character that is not plain printable text escaped,
// such as a right-to-left override. A program chose these bytes, so they are
// never shown raw.
func ProgramCommandText(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if a != "" && plainArg(a) {
			parts[i] = a
			continue
		}
		parts[i] = strconv.QuoteToGraphic(a)
	}
	return strings.Join(parts, " ")
}

// plainArg reports whether a shows the same unquoted: letters, digits and
// punctuation that no shell reads specially.
func plainArg(a string) bool {
	for _, r := range a {
		if r > unicode.MaxASCII || !(isAlnum(byte(r)) || strings.ContainsRune("_@%+=:,./-", r)) {
			return false
		}
	}
	return true
}
