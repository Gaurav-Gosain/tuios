package session

import "sync"

// ViewerReport is what a viewer says about the terminal it shows a pane in.
type ViewerReport struct {
	// Bg, Fg and Palette are #rrggbb colours, and sixteen comma-separated
	// ones, as a client pushes them (SessionState.PaneReportBg and so on).
	// Empty is unknown.
	Bg      string `json:"bg,omitempty"`
	Fg      string `json:"fg,omitempty"`
	Palette string `json:"palette,omitempty"`
	// Kitty and Sixel are the graphics the terminal draws.
	Kitty bool `json:"kitty,omitempty"`
	Sixel bool `json:"sixel,omitempty"`
}

type sessionViewers struct {
	mu    sync.Mutex
	order []string
	by    map[string]ViewerReport
}

func (s *Session) addViewer(holder string, r ViewerReport) {
	s.viewers.mu.Lock()
	if s.viewers.by == nil {
		s.viewers.by = make(map[string]ViewerReport)
	}
	if _, ok := s.viewers.by[holder]; !ok {
		s.viewers.order = append(s.viewers.order, holder)
	}
	s.viewers.by[holder] = r
	s.viewers.mu.Unlock()
	s.applyViewers()
}

func (s *Session) removeViewer(holder string) {
	s.viewers.mu.Lock()
	defer s.viewers.mu.Unlock()
	if _, ok := s.viewers.by[holder]; !ok {
		return
	}
	delete(s.viewers.by, holder)
	for i, h := range s.viewers.order {
		if h == holder {
			s.viewers.order = append(s.viewers.order[:i], s.viewers.order[i+1:]...)
			break
		}
	}
}

func (s *Session) applyViewers() {
	if s.tuiClients.Load() > 0 {
		return
	}
	s.viewers.mu.Lock()
	if len(s.viewers.order) == 0 {
		s.viewers.mu.Unlock()
		return
	}
	newest := s.viewers.by[s.viewers.order[len(s.viewers.order)-1]]
	kitty, sixel := false, false
	for _, r := range s.viewers.by {
		kitty = kitty || r.Kitty
		sixel = sixel || r.Sixel
	}
	s.viewers.mu.Unlock()
	s.applyReportColors(newest.Bg, newest.Fg, newest.Palette)
	s.SetKittyAdvertised(kitty)
	s.SetSixelAdvertised(sixel || kitty)
}

func (s *Session) noteTUIClients(n int) {
	s.tuiClients.Store(int32(n))
	s.ptysMu.RLock()
	ptys := make([]*PTY, 0, len(s.ptys))
	for _, p := range s.ptys {
		ptys = append(ptys, p)
	}
	s.ptysMu.RUnlock()
	for _, p := range ptys {
		p.setNoClients(n == 0)
	}
	if n == 0 {
		s.applyViewers()
	}
}
