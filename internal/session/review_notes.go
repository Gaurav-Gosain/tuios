package session

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/atomicfile"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/review"
)

const reviewNotesMax = 200

const reviewNotesSaveDelay = 500 * time.Millisecond

type reviewPane struct {
	Root   string        `json:"root"`
	Window string        `json:"window"`
	Base   string        `json:"base,omitempty"`
	Notes  []review.Note `json:"notes"`
}

type reviewNoteStore struct {
	mu        sync.Mutex
	panes     map[string]*reviewPane
	nextID    uint64
	path      string
	saveTimer *time.Timer
	frozen    bool
	entries   atomic.Int64
}

func reviewKey(root, window string) string { return root + "\x00" + window }

func reviewNotesPath() string {
	return filepath.Join(getResurrectionDir(), "review", "notes.json")
}

func canonRoot(p string) string {
	if p == "" {
		return ""
	}
	p = filepath.Clean(p)
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	var rest []string
	dir := p
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return p
		}
		rest = append(rest, filepath.Base(dir))
		dir = parent
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			slices.Reverse(rest)
			return filepath.Join(append([]string{real}, rest...)...)
		}
	}
}

func (s *reviewNoteStore) countLocked(root string) int {
	n := 0
	for _, p := range s.panes {
		if p.Root == root {
			n += len(p.Notes)
		}
	}
	return n
}

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

const (
	reviewNoteOK = iota
	reviewNoteMissing
	reviewNoteRefused
)

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

func (s *reviewNoteStore) remove(root, window, id string, may func(review.Note) bool) (removed int, found bool, refused int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := reviewKey(root, window)
	p := s.panes[key]
	if p == nil {
		return 0, false, 0
	}
	kept := p.Notes[:0]
	for _, n := range p.Notes {
		if id != "" && n.ID != id {
			kept = append(kept, n)
			continue
		}
		found = true
		if !may(n) {
			refused++
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

func (s *reviewNoteStore) noteSessionEvent(ev SessionEvent) {
	if ev.Type != EventWindowClosed || s.entries.Load() == 0 {
		return
	}
	s.dropWindow(ev.Window)
}

type reviewNotesFile struct {
	Version int           `json:"version"`
	NextID  uint64        `json:"next_id"`
	Panes   []*reviewPane `json:"panes"`
}

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
	data, err := jsonx.Marshal(f)
	if err != nil {
		return nil
	}
	return data
}

func writeReviewNotes(path string, data []byte) {
	if data == nil {
		return
	}
	if err := atomicfile.WritePrivate(path, data, false); err != nil {
		LogError("Failed to save the review notes: %v", err)
	}
}

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

func (s *reviewNoteStore) load(path string, live func(window string) bool) {
	var f reviewNotesFile
	if data, err := os.ReadFile(path); err == nil {
		if err := jsonx.Unmarshal(data, &f); err != nil || f.Version != 1 {
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
