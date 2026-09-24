package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/review"
)

// The review notes store: the notes people and agents leave on a pane's
// changes, held by the daemon so every client and the CLI see the same ones.
//
// Notes are keyed by the worktree's root (its path with symbolic links
// resolved), then by the pane they are for. A worktree holds at most
// reviewNotesMax notes across its panes. The store is saved to
// <resurrection dir>/review/notes.json, readable by the owner only, half a
// second after a change and once more at shutdown. On load a pane that did not
// come back loses its notes, and so does a worktree whose directory is gone.
// Removing a worktree with remove-worktree (or keep-fan) drops its notes.
//
// A daemon with no notes costs one atomic load per window close.

// reviewNotesMax bounds the notes on one worktree.
const reviewNotesMax = 200

// reviewNotesSaveDelay is how long after a change the store is saved, so a
// burst of edits is one write.
const reviewNotesSaveDelay = 500 * time.Millisecond

// reviewPane is one pane's notes on one worktree.
type reviewPane struct {
	Root   string `json:"root"`
	Window string `json:"window"`
	// Base is the base the pane's last review-diff was read against, which
	// send-review names in the message. Empty until a diff was read.
	Base  string        `json:"base,omitempty"`
	Notes []review.Note `json:"notes"`
}

// reviewNoteStore holds every pane's notes. The zero value is ready and does
// not save; load gives it its file.
type reviewNoteStore struct {
	mu        sync.Mutex
	panes     map[string]*reviewPane
	nextID    uint64
	path      string
	saveTimer *time.Timer
	frozen    bool
	// entries is len(panes), kept so a window close costs one atomic load on
	// a daemon with no notes.
	entries atomic.Int64
}

// reviewKey is the key of a pane's notes on a worktree.
func reviewKey(root, window string) string { return root + "\x00" + window }

// reviewNotesPath is where the store is saved.
func reviewNotesPath() string {
	return filepath.Join(getResurrectionDir(), "review", "notes.json")
}

// canonRoot is a worktree root as notes are keyed by it: symbolic links
// resolved, so a path read from a process and one recorded at creation name
// the same key.
func canonRoot(p string) string {
	if p == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return filepath.Clean(p)
}

// countLocked is how many notes a worktree holds. It holds mu.
func (s *reviewNoteStore) countLocked(root string) int {
	n := 0
	for _, p := range s.panes {
		if p.Root == root {
			n += len(p.Notes)
		}
	}
	return n
}

// list returns a pane's notes in their listed order, and the base its last
// diff was read against.
func (s *reviewNoteStore) list(root, window string) ([]review.Note, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.panes[reviewKey(root, window)]
	if p == nil {
		return []review.Note{}, ""
	}
	out := slices.Clone(p.Notes)
	review.SortNotes(out)
	return out, p.Base
}

// add stores n as a new note of the pane and returns it with its id. It
// refuses a worktree already at reviewNotesMax, returning false.
func (s *reviewNoteStore) add(root, window string, n review.Note) (review.Note, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.countLocked(root) >= reviewNotesMax {
		return review.Note{}, false
	}
	if s.panes == nil {
		s.panes = make(map[string]*reviewPane)
	}
	key := reviewKey(root, window)
	p := s.panes[key]
	if p == nil {
		p = &reviewPane{Root: root, Window: window}
		s.panes[key] = p
	}
	s.nextID++
	n.ID = "n" + strconv.FormatUint(s.nextID, 10)
	p.Notes = append(p.Notes, n)
	s.changedLocked()
	return n, true
}

// reviewNoteMissing and reviewNoteRefused are what edit and remove report
// about a note they did not change.
const (
	reviewNoteOK = iota
	reviewNoteMissing
	reviewNoteRefused
)

// edit replaces the text of note id, when may allows the caller to touch it.
// The note becomes by's, unsent again, and is stamped now.
func (s *reviewNoteStore) edit(root, window, id, text, by string, may func(review.Note) bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.panes[reviewKey(root, window)]
	if p == nil {
		return reviewNoteMissing
	}
	for i := range p.Notes {
		if p.Notes[i].ID != id {
			continue
		}
		if !may(p.Notes[i]) {
			return reviewNoteRefused
		}
		p.Notes[i].Text = text
		p.Notes[i].By = by
		p.Notes[i].At = time.Now().UnixNano()
		p.Notes[i].SentAt = 0
		s.changedLocked()
		return reviewNoteOK
	}
	return reviewNoteMissing
}

// remove drops the notes of the pane that match: every one when id is empty,
// else the one with that id. Notes may refuses are kept. It returns how many
// went and whether any match was refused.
func (s *reviewNoteStore) remove(root, window, id string, may func(review.Note) bool) (removed int, found, refused bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := reviewKey(root, window)
	p := s.panes[key]
	if p == nil {
		return 0, false, false
	}
	kept := p.Notes[:0]
	for _, n := range p.Notes {
		if id != "" && n.ID != id {
			kept = append(kept, n)
			continue
		}
		found = true
		if !may(n) {
			refused = true
			kept = append(kept, n)
			continue
		}
		removed++
	}
	clear(p.Notes[len(kept):])
	p.Notes = kept
	if len(p.Notes) == 0 && p.Base == "" {
		delete(s.panes, key)
	}
	if removed > 0 {
		s.changedLocked()
	}
	return removed, found, refused
}

// markSent stamps the notes named by ids as sent at at.
func (s *reviewNoteStore) markSent(root, window string, ids []string, at int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.panes[reviewKey(root, window)]
	if p == nil {
		return
	}
	for i := range p.Notes {
		if slices.Contains(ids, p.Notes[i].ID) {
			p.Notes[i].SentAt = at
		}
	}
	s.changedLocked()
}

// setBase records the base the pane's diff was last read against.
func (s *reviewNoteStore) setBase(root, window, base string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.panes[reviewKey(root, window)]
	if p == nil {
		if base == "" {
			return
		}
		if s.panes == nil {
			s.panes = make(map[string]*reviewPane)
		}
		p = &reviewPane{Root: root, Window: window}
		s.panes[reviewKey(root, window)] = p
	}
	if p.Base != base {
		p.Base = base
		s.changedLocked()
	}
}

// applyAnchors writes where the notes in moved sit now: line, hunk header and
// outdated, matched by id. A note edited or removed since it was read is
// matched by id still, and its text is left as it is now.
func (s *reviewNoteStore) applyAnchors(root, window string, moved []review.Note) {
	if len(moved) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.panes[reviewKey(root, window)]
	if p == nil {
		return
	}
	for _, m := range moved {
		for i := range p.Notes {
			if p.Notes[i].ID == m.ID {
				p.Notes[i].Line = m.Line
				p.Notes[i].HunkHeader = m.HunkHeader
				p.Notes[i].Outdated = m.Outdated
			}
		}
	}
	s.changedLocked()
}

// dropRoot forgets every note on the worktree at root.
func (s *reviewNoteStore) dropRoot(root string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for key, p := range s.panes {
		if p.Root == root {
			delete(s.panes, key)
			changed = true
		}
	}
	if changed {
		s.changedLocked()
	}
}

// dropWindow forgets the notes for a pane that closed. It runs on the session
// event sink, so it takes only its own lock.
func (s *reviewNoteStore) dropWindow(window string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for key, p := range s.panes {
		if p.Window == window {
			delete(s.panes, key)
			changed = true
		}
	}
	if changed {
		s.changedLocked()
	}
}

// noteSessionEvent drops the notes of a pane that closed. A pane closing on
// shutdown saves nothing: the store froze before any pane closed.
func (s *reviewNoteStore) noteSessionEvent(ev SessionEvent) {
	if ev.Type != EventWindowClosed || s.entries.Load() == 0 {
		return
	}
	s.dropWindow(ev.Window)
}

// reviewNotesFile is the on-disk form of the store.
type reviewNotesFile struct {
	Version int           `json:"version"`
	NextID  uint64        `json:"next_id"`
	Panes   []*reviewPane `json:"panes"`
}

// changedLocked schedules a save. It holds mu.
func (s *reviewNoteStore) changedLocked() {
	s.entries.Store(int64(len(s.panes)))
	if s.path == "" || s.frozen || s.saveTimer != nil {
		return
	}
	s.saveTimer = time.AfterFunc(reviewNotesSaveDelay, func() {
		s.mu.Lock()
		s.saveTimer = nil
		frozen := s.frozen
		data := s.encodeLocked()
		path := s.path
		s.mu.Unlock()
		if !frozen {
			writeReviewNotes(path, data)
		}
	})
}

// encodeLocked serialises the store. It holds mu.
func (s *reviewNoteStore) encodeLocked() []byte {
	f := reviewNotesFile{Version: 1, NextID: s.nextID, Panes: []*reviewPane{}}
	keys := make([]string, 0, len(s.panes))
	for k := range s.panes {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if p := s.panes[k]; len(p.Notes) > 0 || p.Base != "" {
			f.Panes = append(f.Panes, p)
		}
	}
	data, err := json.Marshal(f)
	if err != nil {
		return nil
	}
	return data
}

// writeReviewNotes writes the store atomically, readable by the owner only:
// a note quotes the repository's code.
func writeReviewNotes(path string, data []byte) {
	if data == nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		LogError("Failed to create the review notes directory: %v", err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		LogError("Failed to save the review notes: %v", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		LogError("Failed to save the review notes: %v", err)
	}
}

// saveNowAndFreeze writes the store as it stands and stops saving: the
// daemon's last save, before shutdown closes any pane.
func (s *reviewNoteStore) saveNowAndFreeze() {
	s.mu.Lock()
	if s.saveTimer != nil {
		s.saveTimer.Stop()
		s.saveTimer = nil
	}
	s.frozen = true
	path := s.path
	var data []byte
	if path != "" {
		data = s.encodeLocked()
	}
	s.mu.Unlock()
	if path != "" {
		writeReviewNotes(path, data)
	}
}

// load reads the notes a previous daemon saved and keeps those of panes that
// came back, on worktrees whose directory is still there. live reports whether
// a window is in a live session; it is called with no lock held. Ids carry on
// from the file, so one is never reused.
func (s *reviewNoteStore) load(path string, live func(window string) bool) {
	var f reviewNotesFile
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &f); err != nil || f.Version != 1 {
			LogError("Discarding the saved review notes, they could not be read: %v", err)
			f = reviewNotesFile{}
		}
	}
	var kept []*reviewPane
	for _, p := range f.Panes {
		if p == nil || p.Root == "" || p.Window == "" || !live(p.Window) {
			continue
		}
		if st, err := os.Stat(p.Root); err != nil || !st.IsDir() {
			continue
		}
		kept = append(kept, p)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.path = path
	s.nextID = max(s.nextID, f.NextID)
	if s.panes == nil {
		s.panes = make(map[string]*reviewPane)
	}
	for _, p := range kept {
		key := reviewKey(p.Root, p.Window)
		if _, dup := s.panes[key]; dup {
			continue
		}
		if room := reviewNotesMax - s.countLocked(p.Root); len(p.Notes) > room {
			p.Notes = p.Notes[:max(room, 0)]
		}
		s.panes[key] = p
	}
	s.entries.Store(int64(len(s.panes)))
	if len(kept) != len(f.Panes) {
		s.changedLocked()
	}
}
