package session

import (
	"os"
	"slices"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
	"tuios.dev/progresume"
	"tuios.dev/vt"
)

// Program resume (OSC 7577), the terminal side.
//
// Each local pane gets a token of its own when its process starts, also a
// pane a restore brings back, and the token goes into the environment of
// that first process and nowhere else. The daemon took OSC7577_TOKEN out of
// its own environment when it started (stripInheritedResumeToken), so the
// hooks, plugins and tasks it starts get none, and neither does a pane on
// another machine. The token is kept in daemon memory only: it is not saved,
// logged or shown, and a live upgrade hands it to the next image in the
// handoff, because the pane's process still holds it.
//
// A report reaches the pane's emulator like any other output, and the
// emulator hands an OSC 7577 to the PrivateOSC callback. progresume.Parse
// checks the mac and the time before anything else, so text that only passes
// through the pane cannot make a record. Then the owner: the pid must be the
// pane's shell or a process under it, run as this user. The owner's start
// time is kept with the record, so a pid the kernel gave to another process
// counts as gone.
//
// A pane keeps one record. A report from a process under the current owner
// does not replace it: the specification prefers an ancestor's record, which
// brings back what it started. The record is saved with the window in the
// session's state file (mode 0600), and only there: the live state the
// clients and the other machines get never holds it. A clear, or a rejected
// report, removes it. So does the owner's exit, two seconds after it was
// seen, unless the pane ended first: at logout a program can exit just before
// its terminal does, and the record is what the restore needs then. Closing
// the pane drops it with the window. No other sequence touches it: a reset,
// the alternate screen, an erase or a prompt mark is not signed.
//
// A restore offers the record as agent_resume.go offers a conversation, with
// the same Inbox item and the same ask, auto and off modes. See
// program_resume_restore.go.

// resumeOwnerGrace is how long a record outlives its owner. An owner that
// exits less than this before its pane or the daemon ends still counts as
// running.
const resumeOwnerGrace = 2 * time.Second

// resumeOwnerDepth bounds the walk from a reporting pid up to the pane's
// shell.
const resumeOwnerDepth = 64

// resumeLastTimes bounds how many pids a pane remembers the time of the last
// accepted report for.
const resumeLastTimes = 32

// stripInheritedResumeToken removes a token the daemon inherited, such as the
// one the terminal it was started from gave its first process. A terminal
// must not pass its own token on, and every process the daemon starts takes
// its environment from the daemon's.
func stripInheritedResumeToken() {
	_ = os.Unsetenv(progresume.TokenEnv)
}

// newResumeToken is a fresh token for one pane, or "" when the setting is off
// or no randomness can be had. A pane with no token never reports.
func (s *Session) newResumeToken(host string) string {
	if host != "" || s.config == nil || s.config.ProgramResume == nil || !s.config.ProgramResume() {
		return ""
	}
	tok, err := progresume.NewToken()
	if err != nil {
		LogError("No program resume token for a new pane: %v", err)
		return ""
	}
	return tok
}

// paneResume is one pane's token and record.
type paneResume struct {
	token string

	mu  sync.Mutex
	rec *resumeRecord
	// last is the time of the last accepted report, by pid. A report older
	// than that is ignored, so a report replayed inside the time window
	// cannot undo a later one.
	last map[int]int64
	// gone is the timer that drops the record after its owner exited.
	gone *time.Timer
}

// resumeRecord is an accepted report.
type resumeRecord struct {
	pid int
	// start is the owner's start time, 0 where it cannot be read.
	start uint64
	argv  []string
	cwd   string
	// dir is the owner's folder when the report named no cwd, read when the
	// report was accepted.
	dir string
	uid int
	// goneAt is when the owner was first seen gone.
	goneAt time.Time
}

func newPaneResume(token string) *paneResume {
	if token == "" {
		return nil
	}
	return &paneResume{token: token}
}

// env is the variable the pane's first process gets, nil without a token.
func (r *paneResume) env() []string {
	if r == nil {
		return nil
	}
	return []string{progresume.TokenEnv + "=" + r.token}
}

// note reads one OSC 7577 payload the pane's emulator handed over. It runs on
// the vtWriter goroutine with the terminal lock held, and does no more than
// read a few process files after the mac has passed.
func (r *paneResume) note(p *PTY, payload []byte, now time.Time) {
	if r == nil {
		return
	}
	res := progresume.Parse(payload, r.token, now)
	if res.Outcome == progresume.Ignored {
		debugLog("[DEBUG] OSC 7577 in %s ignored: %s", shortID(p.windowID), res.Reason)
		return
	}
	rep := res.Message
	r.mu.Lock()
	defer r.mu.Unlock()
	if rep.PID > 0 && rep.Time < r.last[rep.PID] {
		debugLog("[DEBUG] OSC 7577 in %s ignored: older than the last report of its owner", shortID(p.windowID))
		return
	}
	if res.Outcome == progresume.Rejected {
		debugLog("[DEBUG] OSC 7577 in %s rejected: %s", shortID(p.windowID), res.Reason)
		r.dropOwnerLocked(p, rep.PID)
		return
	}
	owner, ok := resumeOwner(rep.PID, p.ShellPID())
	if !ok {
		debugLog("[DEBUG] OSC 7577 in %s rejected: the pid is not a process of the pane", shortID(p.windowID))
		r.dropOwnerLocked(p, rep.PID)
		return
	}
	r.noteTimeLocked(rep.PID, rep.Time)
	if rep.Clear {
		r.dropOwnerLocked(p, rep.PID)
		return
	}
	if cur := r.rec; cur != nil && cur.pid != rep.PID && cur.goneAt.IsZero() && ownerAlive(cur) && descendsFrom(rep.PID, cur.pid) {
		debugLog("[DEBUG] OSC 7577 in %s kept the record of the process above the reporter", shortID(p.windowID))
		return
	}
	rec := &resumeRecord{pid: rep.PID, start: owner.start, argv: slices.Clone(rep.Argv), cwd: rep.Cwd, uid: owner.uid}
	if rec.cwd == "" {
		rec.dir, _ = ptyspawn.ProcessCwd(rep.PID)
	}
	r.setLocked(p, rec)
}

// noteTimeLocked records the time of an accepted report.
func (r *paneResume) noteTimeLocked(pid int, t int64) {
	if r.last == nil || len(r.last) >= resumeLastTimes {
		r.last = make(map[int]int64, 4)
	}
	r.last[pid] = t
}

// dropOwnerLocked removes the record of pid, when it holds it.
func (r *paneResume) dropOwnerLocked(p *PTY, pid int) {
	if pid <= 0 || r.rec == nil || r.rec.pid != pid {
		return
	}
	r.setLocked(p, nil)
}

// setLocked replaces the record and has the session saved.
func (r *paneResume) setLocked(p *PTY, rec *resumeRecord) {
	if r.gone != nil {
		r.gone.Stop()
		r.gone = nil
	}
	r.rec = rec
	if s := p.owner(); s != nil {
		s.stateDirty.Set()
	}
}

// noteMark looks at the owner when the shell prints its prompt, which is
// when a program it ran has just ended.
func (r *paneResume) noteMark(p *PTY, m vt.SemanticMarker) {
	if r == nil || m.Type != vt.MarkerPromptStart {
		return
	}
	r.checkOwner(p, time.Now())
}

// checkOwner notes that the owner of the record has exited, and drops the
// record resumeOwnerGrace later unless the pane ended first.
func (r *paneResume) checkOwner(p *PTY, now time.Time) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.rec
	if rec == nil || !rec.goneAt.IsZero() || ownerAlive(rec) {
		return
	}
	rec.goneAt = now
	r.gone = time.AfterFunc(resumeOwnerGrace, func() {
		if p.IsExited() {
			return
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.rec == rec {
			r.setLocked(p, nil)
		}
	})
}

// saved is the record as the state file keeps it, or nil. An owner gone for
// longer than resumeOwnerGrace has nothing left to resume.
func (r *paneResume) saved(p *PTY, now time.Time) *SavedProgram {
	if r == nil {
		return nil
	}
	r.checkOwner(p, now)
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.rec
	if rec == nil || (!rec.goneAt.IsZero() && now.Sub(rec.goneAt) >= resumeOwnerGrace) {
		return nil
	}
	return &SavedProgram{Argv: slices.Clone(rec.argv), Cwd: rec.cwd, Dir: rec.dir, UID: rec.uid}
}

// SavedProgram is a pane's program resume record in the session's state
// file. It is written to the file and to nothing else: the live state never
// holds it.
type SavedProgram struct {
	// Argv is the command the program reported.
	Argv []string `json:"argv"`
	// Cwd is the folder the report named. A restore does not resume a
	// record whose folder is gone.
	Cwd string `json:"cwd,omitempty"`
	// Dir is the owner's folder when the report named none.
	Dir string `json:"dir,omitempty"`
	// UID is the user the report came from. A record is resumed only as the
	// same user.
	UID int `json:"uid"`
}

// resumeOwnerInfo is what the owner check read.
type resumeOwnerInfo struct {
	start uint64
	uid   int
}

// resumeOwner checks that pid is the pane's shell or runs under it, as this
// user, and reads its start time. Where processes cannot be looked up at all
// the check is skipped, as the specification allows, and the record has no
// start time, so it never counts as gone.
func resumeOwner(pid, shell int) (resumeOwnerInfo, bool) {
	if pid <= 0 || shell <= 0 {
		return resumeOwnerInfo{}, false
	}
	if _, can := procinfo.StartTime(os.Getpid()); !can {
		return resumeOwnerInfo{uid: os.Getuid()}, true
	}
	start, ok := procinfo.StartTime(pid)
	if !ok {
		return resumeOwnerInfo{}, false
	}
	uid, ok := procinfo.UID(pid)
	if !ok || uid != os.Getuid() {
		return resumeOwnerInfo{}, false
	}
	if pid != shell && !descendsFrom(pid, shell) {
		return resumeOwnerInfo{}, false
	}
	return resumeOwnerInfo{start: start, uid: uid}, true
}

// descendsFrom reports whether pid runs under root, walking up at most
// resumeOwnerDepth parents.
func descendsFrom(pid, root int) bool {
	cur := pid
	for range resumeOwnerDepth {
		ppid, ok := procinfo.ParentPID(cur)
		if !ok || ppid <= 0 || ppid == cur {
			return false
		}
		if ppid == root {
			return true
		}
		cur = ppid
	}
	return false
}

// ownerAlive reports whether the owner still runs: its pid names a process
// with the start time it had. A record with no start time counts as alive.
func ownerAlive(rec *resumeRecord) bool {
	if rec.start == 0 {
		return true
	}
	st, ok := procinfo.StartTime(rec.pid)
	return ok && st == rec.start
}

// handoffResume is a pane's token and record, carried over a live upgrade.
// The pane's process keeps running and keeps the token, so the next image
// must check its reports with the same one.
type handoffResume struct {
	Token string
	PID   int
	Start uint64
	Argv  []string
	Cwd   string
	Dir   string
	UID   int
	Last  map[int]int64
}

// handoffState is the pane's resume state for the handoff, nil without a
// token.
func (r *paneResume) handoffState() *handoffResume {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	h := &handoffResume{Token: r.token, Last: r.last}
	if rec := r.rec; rec != nil && rec.goneAt.IsZero() {
		h.PID, h.Start, h.Argv, h.Cwd, h.Dir, h.UID = rec.pid, rec.start, rec.argv, rec.cwd, rec.dir, rec.uid
	}
	return h
}

// restoreResume is the resume state a handoff carried, nil for none.
func restoreResume(h *handoffResume) *paneResume {
	if h == nil || !progresume.ValidToken(h.Token) {
		return nil
	}
	r := &paneResume{token: h.Token, last: h.Last}
	if len(h.Argv) > 0 {
		r.rec = &resumeRecord{pid: h.PID, start: h.Start, argv: h.Argv, cwd: h.Cwd, dir: h.Dir, uid: h.UID}
	}
	return r
}
