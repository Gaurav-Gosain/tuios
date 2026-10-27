package learn

import (
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/review"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/webshell"
)

// The files, the review and the worktrees of the tour. The rail reads the
// fake shell's files, ctrl+b U makes a worktree of its repository, and the
// review overlay reads that repository's diff through the same verbs it asks
// a daemon for on a real machine.

// backend is the fake shell's files and repository, as app.LearnBackend.
type backend struct{}

func (backend) ReadDir(dir string, limit int) ([]os.DirEntry, bool, error) {
	return webshell.ReadDir(dir, limit)
}
func (backend) RepoRoot(dir string) string { return webshell.RepoRoot(dir) }
func (backend) NewWorktree(repoRoot, branch string) (string, string, error) {
	return webshell.NewWorktree(repoRoot, branch)
}

// verbFunc answers one verb, as the daemon would.
type verbFunc func(params map[string]any) (any, error)

// callVerb is the tour's daemon: the review verbs in m.verbs, and the fleet
// for the rest, which answers the agents view's verbs and refuses any other.
// The app calls it off its loop, in a command.
func (m *Model) callVerb(verb string, params map[string]any, timeout time.Duration) (json.RawMessage, error) {
	fn, ok := m.verbs[verb]
	if !ok {
		return m.fleet.call(verb, params, timeout)
	}
	res, err := fn(params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(res)
}

// notes are the review notes left in the tab, by the session the checkout's
// panes are in on a real machine.
type notes struct {
	mu   sync.Mutex
	by   map[string][]review.Note
	next int
}

// registerReviewVerbs fakes review-diff, review-note and send-review over the
// fake repository. compare-fan is the fleet's: a pane that is no attempt of
// a fan gets its refusal, which the review reads as no fan.
func (m *Model) registerReviewVerbs() {
	n := &notes{by: map[string][]review.Note{}}
	str := func(p map[string]any, k string) string {
		s, _ := p[k].(string)
		return s
	}
	m.verbs["review-diff"] = func(p map[string]any) (any, error) {
		uncommitted, _ := p["uncommitted"].(bool)
		if str(p, "against") != "" {
			return nil, &session.VerbCallError{Code: session.ErrVerbNotRepo, Message: "the demo has no fan to compare"}
		}
		r, err := webshell.ReviewDiff(str(p, "dir"), str(p, "base"), uncommitted)
		if err != nil {
			return nil, &session.VerbCallError{Code: session.ErrVerbNotRepo, Message: err.Error()}
		}
		totals := review.Totals{Files: len(r.Files)}
		for _, f := range r.Files {
			totals.Added += f.Added
			totals.Removed += f.Removed
		}
		files := r.Files
		if files == nil {
			files = []review.File{}
		}
		n.mu.Lock()
		kept := slices.Clone(n.by[r.Session])
		n.mu.Unlock()
		return map[string]any{
			"session": r.Session, "window": str(p, "window"),
			"repo_root": r.RepoRoot, "worktree": r.Root,
			"base": r.Base, "base_sha": r.BaseHash, "uncommitted": r.Uncommitted,
			"files": files, "totals": totals, "truncated": false, "notes": kept,
		}, nil
	}
	m.verbs["review-note"] = func(p map[string]any) (any, error) {
		sess := str(p, "session")
		n.mu.Lock()
		defer n.mu.Unlock()
		list := n.by[sess]
		event := map[string]any{"action": str(p, "action")}
		switch str(p, "action") {
		case "add":
			n.next++
			line, _ := p["line"].(int)
			if f, ok := p["line"].(float64); ok {
				line = int(f)
			}
			note := review.Note{
				ID: "n" + strconv.Itoa(n.next), Path: str(p, "path"), Side: str(p, "side"),
				Line: line, Quote: str(p, "quote"), HunkHeader: str(p, "hunk"),
				Text: review.CleanText(str(p, "text")), By: "human", At: time.Now().UnixNano(),
			}
			list = append(list, note)
			event["path"], event["line"], event["text"] = note.Path, note.Line, note.Text
		case "edit":
			i := slices.IndexFunc(list, func(x review.Note) bool { return x.ID == str(p, "id") })
			if i < 0 {
				return nil, &session.VerbCallError{Code: session.ErrVerbNoNotes, Message: "no such note"}
			}
			list[i].Text, list[i].At, list[i].SentAt = review.CleanText(str(p, "text")), time.Now().UnixNano(), 0
			event["path"], event["line"], event["text"] = list[i].Path, list[i].Line, list[i].Text
		case "remove":
			list = slices.DeleteFunc(list, func(x review.Note) bool { return x.ID == str(p, "id") })
		default:
			return nil, &session.VerbCallError{Code: "bad_params", Message: "unknown action"}
		}
		n.by[sess] = list
		event["notes"] = len(list)
		m.emit(Event{Type: EventReviewNote, Data: event})
		return map[string]any{"notes": slices.Clone(list)}, nil
	}
	m.verbs["send-review"] = func(p map[string]any) (any, error) {
		sess := str(p, "session")
		n.mu.Lock()
		defer n.mu.Unlock()
		var ids []string
		now := time.Now().UnixNano()
		for i := range n.by[sess] {
			if note := &n.by[sess][i]; note.SentAt == 0 {
				note.SentAt = now
				ids = append(ids, note.ID)
			}
		}
		if len(ids) == 0 {
			return nil, &session.VerbCallError{Code: session.ErrVerbNoNotes, Message: "no unsent notes"}
		}
		m.emit(Event{Type: EventReviewSend, Data: map[string]any{"notes": len(ids)}})
		return map[string]any{"notes": len(ids), "ids": ids, "position": 0, "delivering": true}, nil
	}
}
