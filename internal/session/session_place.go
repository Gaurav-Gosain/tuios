package session

import (
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Gaurav-Gosain/tuios/internal/winpath"
)

// A session's place.
//
// With six sessions open the rail read "session-0" to "session-5", which tells
// a person nothing. The daemon already hears where every pane's shell is: fish
// and a growing number of prompts report the directory over OSC 7 on every cd,
// and the spawn directory is known before the shell prints a prompt. This file
// turns that into two facts per pane, the directory and the git branch it is
// in, and Info publishes the focused pane's pair so the rail can label an
// unnamed session by where it is.
//
// Both facts are recorded on the cwd change and at no other time. There is no
// timer and nothing here runs on a frame: a rail that re-read git state on
// every tick is exactly the idle cost this project measures and refuses. The
// branch read walks up from the directory looking for .git and reads HEAD, off
// the emulator goroutine, because a directory on a slow network mount would
// otherwise stall every byte the pane prints.

// placeRecord is what a PTY knows about where its shell is.
type placeRecord struct {
	cwd    atomic.Pointer[string]
	branch atomic.Pointer[string]
	// gen counts cwd changes, so a branch read that finishes after a later
	// change has already landed is dropped rather than stored over it.
	gen atomic.Uint64
	// announced says the shell itself reported cwd over OSC 7, rather than
	// cwd being the spawn directory it was seeded with. A shell that reports
	// its directory is the best answer there is; one that never has is read
	// from its process instead. See Session.livePlaces.
	announced atomic.Bool
	// elsewhere names the machine the last OSC 7 report came from, when that
	// is not this one: the pane is running ssh and the shell on the far end
	// reported its folder. Empty otherwise. It holds while the program that
	// made the report holds the terminal; see Session.checkPaneCwd.
	elsewhere atomic.Pointer[string]
	// elsewhereDir is the folder that report named on that machine. An ssh
	// split starts the new pane there. See ssh_follow.go.
	elsewhereDir atomic.Pointer[string]
}

// setCwd records a directory the shell reported (an OSC 7 payload or a bare
// path) and starts one branch read for it. A payload that names another
// machine is ignored: that is an ssh shell inside the pane, and its directory
// is not one this daemon can read.
func (r *placeRecord) setCwd(raw string) {
	path, ok := parseCwdReport(raw)
	if !ok {
		return
	}
	r.setCwdPath(path)
}

// announce records what the shell reported over OSC 7, and says whether the
// pane's place changed. Unlike setCwd it keeps a report that names another
// machine, as the machine the pane's shell is now on.
func (r *placeRecord) announce(raw string) bool {
	path, host, ok := parseCwdAnnouncement(raw)
	if !ok {
		return false
	}
	if host != "" {
		r.elsewhereDir.Store(&path)
		return r.setElsewhere(host)
	}
	moved := r.setElsewhere("")
	if !r.announced.Swap(true) {
		moved = true
	}
	if r.setCwdPath(path) {
		moved = true
	}
	return moved
}

// Elsewhere is the machine the pane's shell last reported a folder on, when
// that is not this one.
func (r *placeRecord) Elsewhere() string {
	if p := r.elsewhere.Load(); p != nil {
		return *p
	}
	return ""
}

// ElsewhereDir is the folder the last report from another machine named.
// It means something only while Elsewhere is not empty.
func (r *placeRecord) ElsewhereDir() string {
	if p := r.elsewhereDir.Load(); p != nil {
		return *p
	}
	return ""
}

// setElsewhere records the machine, or clears it, and says whether it changed.
func (r *placeRecord) setElsewhere(host string) bool {
	if r.Elsewhere() == host {
		return false
	}
	r.elsewhere.Store(&host)
	return true
}

// announcedCwd is the directory the shell reported, empty when it has never
// reported one.
func (r *placeRecord) announcedCwd() string {
	if !r.announced.Load() {
		return ""
	}
	return r.Cwd()
}

// setCwdPath stores a directory and starts one branch read for it. It says
// whether the directory changed.
func (r *placeRecord) setCwdPath(path string) bool {
	if cur := r.cwd.Load(); cur != nil && *cur == path {
		return false
	}
	r.cwd.Store(&path)
	// The old branch is wrong the moment the directory changes. Clear it now
	// rather than let it ride a directory it does not describe while the read
	// is in flight.
	empty := ""
	r.branch.Store(&empty)
	gen := r.gen.Add(1)
	go func() {
		b := gitBranch(path)
		if r.gen.Load() == gen {
			r.branch.Store(&b)
		}
	}()
	return true
}

// Cwd is the directory the shell last reported, or empty when it never has.
func (r *placeRecord) Cwd() string {
	if p := r.cwd.Load(); p != nil {
		return *p
	}
	return ""
}

// Branch is the git branch of the reported directory, empty outside a checkout
// or while the read for the latest directory has not landed yet.
func (r *placeRecord) Branch() string {
	if p := r.branch.Load(); p != nil {
		return *p
	}
	return ""
}

// place is the pair the listing carries: the directory as a label, and the
// branch.
func (r *placeRecord) place() (dir, branch string) {
	return dirLabel(r.Cwd(), homeDir()), r.Branch()
}

// homeDir is the daemon's home directory, read once. It is what "~" stands for
// in a label, and it is the daemon's home rather than a client's because the
// directory belongs to a shell this daemon spawned.
var homeDir = sync.OnceValue(func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Clean(home)
})

// localHostname is this machine's name, read once, for telling a local OSC 7
// payload from a remote one.
var localHostname = sync.OnceValue(func() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return strings.ToLower(h)
})

// parseCwdReport reads a local absolute path out of what a shell reported. OSC 7
// carries a file://host/path URI; a bare absolute path is accepted too, which is
// what the spawn directory and some prompts are. Anything else, including a
// path on another host, is not a directory here.
func parseCwdReport(raw string) (string, bool) {
	path, host, ok := parseCwdAnnouncement(raw)
	if !ok || host != "" {
		return "", false
	}
	return path, true
}

// ParseCwdAnnouncement is parseCwdAnnouncement for a client that judges a
// report from a pane it runs itself, so both sides read OSC 7 the same way.
func ParseCwdAnnouncement(raw string) (path, host string, ok bool) {
	return parseCwdAnnouncement(raw)
}

// parseCwdAnnouncement reads a report the way parseCwdReport does, and keeps one
// that names another machine. host is that machine, empty for this one.
func parseCwdAnnouncement(raw string) (path, host string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	if !strings.HasPrefix(raw, "file://") {
		if p := winpath.Native(raw); filepath.IsAbs(p) {
			return filepath.Clean(p), "", true
		}
		return "", "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		return "", "", false
	}
	if h := strings.ToLower(u.Hostname()); h != "" && h != "localhost" && h != localHostname() {
		// Bounded: it is shown on a rail row, and the pane wrote it. The path
		// is another machine's, so it is not converted for this one.
		return filepath.Clean(u.Path), ClampDisplayText(u.Hostname()), true
	}
	// An MSYS2 or Cygwin shell on Windows reports /c/Users/x, which a native
	// program reads as \c\Users\x. winpath.Native makes it C:\Users\x, and
	// leaves the path alone on every other platform.
	return filepath.Clean(winpath.Native(u.Path)), "", true
}

// dirLabel is the short form of a directory for a row: its base name, or "~"
// for the home directory itself. A subdirectory of home keeps its own name,
// because "~/x" is longer and says the same thing a base name does.
func dirLabel(cwd, home string) string {
	if cwd == "" {
		return ""
	}
	if home != "" && cwd == home {
		return "~"
	}
	return filepath.Base(cwd)
}

// gitHeadLimit bounds how much of a .git file or HEAD is read. Both are one
// line, and a directory that puts something else under those names is not a
// checkout worth stalling on.
const gitHeadLimit = 4096

// gitBranch is the branch checked out at dir or the nearest parent that is a
// checkout, empty when none is. A detached HEAD reads as its short hash, which
// is what git itself shows for one. The walk is bounded so a pathological path
// cannot make it long.
func gitBranch(dir string) string {
	for range 64 {
		if head := gitHeadPath(dir); head != "" {
			return readHeadRef(head)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
}

// gitHeadPath is where HEAD lives for a checkout rooted at dir, or empty when
// dir is not a checkout root. A plain checkout keeps it under the .git
// directory. A worktree or submodule has a .git file instead, whose one line
// names the directory that holds this checkout's HEAD.
func gitHeadPath(dir string) string {
	dotGit := filepath.Join(dir, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return filepath.Join(dotGit, "HEAD")
	}
	line := readFirstLine(dotGit)
	gitdir, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return ""
	}
	gitdir = strings.TrimSpace(gitdir)
	if gitdir == "" {
		return ""
	}
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(dir, gitdir)
	}
	return filepath.Join(gitdir, "HEAD")
}

// readHeadRef reads a branch name out of a HEAD file.
func readHeadRef(head string) string {
	line := readFirstLine(head)
	if ref, ok := strings.CutPrefix(line, "ref:"); ok {
		return strings.TrimPrefix(strings.TrimSpace(ref), "refs/heads/")
	}
	if len(line) >= 40 && isHex(line) {
		return line[:7]
	}
	return ""
}

// readFirstLine is the first line of a small file, trimmed, or empty on any
// error. It reads at most gitHeadLimit bytes.
func readFirstLine(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }() // opened read-only: nothing to flush
	data, err := io.ReadAll(io.LimitReader(f, gitHeadLimit))
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(string(data), "\n")
	return strings.TrimSpace(line)
}

func isHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// generatedSessionName matches the names GenerateSessionName hands out.
var generatedSessionName = regexp.MustCompile(`^session-[0-9]+$`)

// IsGeneratedSessionName reports whether a session name is one tuios made up
// rather than one a person chose. A surface labels such a session by where it
// is, because "session-3" says nothing and a directory says something.
func IsGeneratedSessionName(name string) bool {
	return generatedSessionName.MatchString(name)
}
