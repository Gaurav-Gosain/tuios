package session

import (
	"os"
	"path/filepath"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/transcriptview"
)

const (
	viewStartWindow = 60 * time.Second
	viewStartSlack  = 2 * time.Second
)

var viewSearchSteps = []time.Duration{10 * time.Second, 30 * time.Second, 60 * time.Second}

type viewSearch struct {
	step    int
	next    time.Time
	stateAt int64
}

type viewPane struct {
	sess     *Session
	windowID string
	ptyID    string
	harness  string
	tr       *harness.Transcript
	id       agentIdentity
	joined   string
	exact    bool
	due      bool
}

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

func (d *Daemon) searchViewTranscripts(reg *harness.Registry) bool {
	if reg == nil {
		return false
	}
	panes, anyDue, later := d.viewPanes(reg, time.Now())
	if !anyDue {
		return later
	}
	type groupKey struct{ harness, cwd string }
	dueGroups := map[groupKey]bool{}
	for i := range panes {
		p := &panes[i]
		p.id = d.paneAgentIdentifier(p.sess)(p.ptyID)
		if p.due && p.id.cwd != "" {
			dueGroups[groupKey{p.harness, p.id.cwd}] = true
		}
	}
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
				continue
			}
			_ = p.sess.JoinAgentTranscriptAs(p.windowID, p.harness, p.tr.Reader, best, false)
			joinedBy[best] = p
		}
	}
	return later
}

type viewFile struct {
	path  string
	start time.Time
}

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
