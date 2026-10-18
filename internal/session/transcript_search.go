package session

import (
	"os"
	"path/filepath"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/transcriptview"
)

// The hookless join for the formats other than Claude Code's (Codex, Gemini
// CLI, Qwen Code). These harnesses keep every session of a project in one
// folder, so "the file of this directory" is not one file, and two agents in
// one folder see the same candidates. A file is given to a pane by the
// session's own start, which the file writes near its start (Identify), set
// against the start of the pane's agent process:
//
//   - A file whose session started within viewStartWindow after a pane's
//     agent started, or viewStartSlack before it, is that pane's candidate.
//   - A file that is the candidate of two panes is no one's. Neither is a
//     file another pane has joined.
//   - A pane takes its newest candidate.
//
// The search runs across every session of the daemon, since two panes in
// one folder may sit in two sessions. A pane searches again on a backoff
// (viewSearchSteps) and then not until its agent state changes, so a better
// candidate that appears later (the pane's own file, after a sibling's was
// the only one) still replaces a searched join. A join the harness's hook
// reported is never replaced here.
//
// The search reads little: one Lstat per file of the folder, and the start of
// a file only when it was written since the earliest agent of the folder
// started.

const (
	// viewStartWindow is how long after its process starts an agent may
	// start the session it writes.
	viewStartWindow = 60 * time.Second
	// viewStartSlack allows for a session time stamped a little before the
	// process start, by clocks of different grain.
	viewStartSlack = 2 * time.Second
)

// viewSearchSteps are the waits between a pane's searches, after the first.
// After the last, the pane searches again only when its agent state changes.
var viewSearchSteps = []time.Duration{10 * time.Second, 30 * time.Second, 60 * time.Second}

// viewSearch is one window's place on the backoff.
type viewSearch struct {
	step    int
	next    time.Time
	stateAt int64
}

// viewPane is a pane whose harness reads a view-only transcript.
type viewPane struct {
	sess     *Session
	windowID string
	ptyID    string
	harness  string
	tr       *harness.Transcript
	id       agentIdentity
	// joined is the file the pane is joined to, "" for none, and exact says
	// the harness's hook named it.
	joined string
	exact  bool
	due    bool
}

// viewDue reports whether a window searches now, and moves it along the
// backoff when it does. A change of the agent's state starts the backoff
// again.
func (s *Session) viewDue(windowID string, stateAt int64, now time.Time) (due, later bool) {
	s.transcripts.mu.Lock()
	defer s.transcripts.mu.Unlock()
	if s.transcripts.viewTry == nil {
		s.transcripts.viewTry = make(map[string]*viewSearch)
	}
	v, ok := s.transcripts.viewTry[windowID]
	if !ok || v.stateAt != stateAt {
		v = &viewSearch{stateAt: stateAt, next: now}
		s.transcripts.viewTry[windowID] = v
	}
	if v.step > len(viewSearchSteps) {
		return false, false
	}
	if now.Before(v.next) {
		return false, true
	}
	if v.step < len(viewSearchSteps) {
		v.next = now.Add(viewSearchSteps[v.step])
	}
	v.step++
	return true, v.step <= len(viewSearchSteps)
}

// viewPanes lists the panes of every session whose harness has a view-only
// transcript that can be searched for, with which of them search now. later
// says a search is due after this one.
func (d *Daemon) viewPanes(reg *harness.Registry, now time.Time) (panes []viewPane, anyDue, later bool) {
	for _, sess := range d.manager.AllSessions() {
		type win struct {
			id, pty, harness string
			stateAt          int64
		}
		var wins []win
		sess.stateMu.RLock()
		for i := range sess.state.Windows {
			w := &sess.state.Windows[i]
			if w.AgentHarness != "" && w.PTYID != "" {
				wins = append(wins, win{w.ID, w.PTYID, w.AgentHarness, w.AgentStateAt})
			}
		}
		sess.stateMu.RUnlock()
		for _, w := range wins {
			tr := reg.TranscriptFor(w.harness)
			if tr == nil || tr.Reader == harness.ReaderJSONL || !tr.Verifies("cwd") {
				continue
			}
			p := viewPane{sess: sess, windowID: w.id, ptyID: w.pty, harness: w.harness, tr: tr}
			sess.transcripts.mu.Lock()
			if j, ok := sess.transcripts.joins[w.id]; ok {
				p.joined, p.exact = j.reader.Path(), j.exact
			}
			sess.transcripts.mu.Unlock()
			if !p.exact {
				due, more := sess.viewDue(w.id, w.stateAt, now)
				p.due = due
				anyDue = anyDue || due
				later = later || more
			}
			panes = append(panes, p)
		}
	}
	return panes, anyDue, later
}

// searchViewTranscripts runs the hookless search for the view-only formats.
// It reports whether a pane will search again on its backoff, so the
// detection tick keeps running until then.
func (d *Daemon) searchViewTranscripts(reg *harness.Registry) bool {
	if reg == nil {
		return false
	}
	panes, anyDue, later := d.viewPanes(reg, time.Now())
	if !anyDue {
		return later
	}
	// Every pane's agent identity, read only for a folder with a pane to
	// search for.
	type groupKey struct{ harness, cwd string }
	dueGroups := map[groupKey]bool{}
	for i := range panes {
		p := &panes[i]
		p.id = d.paneAgentIdentifier(p.sess)(p.ptyID)
		if p.due && p.id.cwd != "" {
			dueGroups[groupKey{p.harness, p.id.cwd}] = true
		}
	}
	// Every file joined anywhere, and by whom.
	joinedBy := map[string]*viewPane{}
	for i := range panes {
		if panes[i].joined != "" {
			joinedBy[panes[i].joined] = &panes[i]
		}
	}
	others := d.allJoinedPaths()
	for key := range dueGroups {
		var group []*viewPane
		for i := range panes {
			if panes[i].harness == key.harness && panes[i].id.cwd == key.cwd {
				group = append(group, &panes[i])
			}
		}
		owner := viewCandidates(group, key.cwd, func(path string) bool {
			// A file joined by a pane outside the group (another harness,
			// another folder) is not this group's to give.
			p, ok := joinedBy[path]
			return others[path] && (!ok || p.harness != key.harness || p.id.cwd != key.cwd)
		})
		ownerOf := make(map[string]*viewPane, len(owner))
		for p, f := range owner {
			ownerOf[f] = p
		}
		for _, p := range group {
			if !p.due || p.exact {
				continue
			}
			best, ok := owner[p]
			if !ok {
				if q := ownerOf[p.joined]; p.joined != "" && q != nil && q != p {
					// The file this pane found earlier belongs to a
					// sibling: the session started with that one.
					p.sess.DropAgentTranscript(p.windowID)
					delete(joinedBy, p.joined)
					p.joined = ""
				}
				continue
			}
			if best == p.joined {
				continue
			}
			if q, taken := joinedBy[best]; taken && q != p {
				// A sibling holds it by an earlier search that a newer
				// file has not displaced yet. Leave it to that pane.
				continue
			}
			_ = p.sess.JoinAgentTranscriptAs(p.windowID, p.harness, p.tr.Reader, best, false)
			joinedBy[best] = p
		}
	}
	return later
}

// viewFile is a candidate file and the session start it says.
type viewFile struct {
	path  string
	start time.Time
}

// viewCandidates gives each pane of one harness and folder its newest file:
// a file whose session started near the pane's agent and near no other
// pane's. taken says a file belongs to a pane outside the group.
func viewCandidates(group []*viewPane, cwd string, taken func(string) bool) map[*viewPane]string {
	if len(group) == 0 {
		return nil
	}
	tr := group[0].tr
	dir := tr.ExpandDir(cwd)
	if dir == "" {
		return nil
	}
	var earliest time.Time
	for _, p := range group {
		if !p.id.started.IsZero() && (earliest.IsZero() || p.id.started.Before(earliest)) {
			earliest = p.id.started
		}
	}
	if earliest.IsZero() {
		return nil
	}
	paths, err := filepath.Glob(filepath.Join(dir, tr.Glob))
	if err != nil {
		return nil
	}
	root := tr.Root()
	var files []viewFile
	for _, path := range paths {
		if !tr.MatchesGlob(root, path) || taken(path) {
			continue
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		// A file not written since the earliest agent of the folder started
		// is an older session's. Its head is not read.
		if info.ModTime().Before(earliest.Add(-viewStartSlack)) {
			continue
		}
		id, ok := transcriptview.Identify(tr.Reader, path)
		if !ok || !id.In(cwd) {
			continue
		}
		files = append(files, viewFile{path: path, start: id.Start})
	}
	owner := map[*viewPane]string{}
	ownerStart := map[*viewPane]time.Time{}
	for _, f := range files {
		var cand *viewPane
		n := 0
		for _, p := range group {
			if p.id.started.IsZero() {
				continue
			}
			if p.exact && p.joined != f.path {
				// The hook named this pane's own file, and it is another.
				continue
			}
			if f.start.Before(p.id.started.Add(-viewStartSlack)) || f.start.After(p.id.started.Add(viewStartWindow)) {
				continue
			}
			cand = p
			n++
		}
		if n != 1 {
			continue
		}
		if prev, ok := ownerStart[cand]; !ok || f.start.After(prev) {
			owner[cand], ownerStart[cand] = f.path, f.start
		}
	}
	return owner
}

// allJoinedPaths is every file a window of any session is joined to.
func (d *Daemon) allJoinedPaths() map[string]bool {
	out := map[string]bool{}
	for _, sess := range d.manager.AllSessions() {
		sess.transcripts.mu.Lock()
		for _, j := range sess.transcripts.joins {
			out[j.reader.Path()] = true
		}
		sess.transcripts.mu.Unlock()
	}
	return out
}
