package session

import "sync"

// Viewers: streams that show a pane on their own.
//
// A daemon session answers a program's terminal queries from its own
// emulator: OSC 10, 11 and 4 with the colours the last client pushed, DA1 and
// the kitty graphics query with what the attached clients' terminals draw.
// With no client attached those answers are left over from the last client,
// or the emulator's defaults (black, white, sixel), while the terminal the
// person looks at is the one a viewer such as tuios pane-view runs in. So a
// viewer reports that terminal when it opens its stream, and while no TUI
// client shows the session the daemon answers from the newest viewer's
// report. A client that attaches pushes its own and wins; when the last
// client leaves, the viewers' report applies again.

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

// sessionViewers holds a session's viewer reports in the order they came.
type sessionViewers struct {
	mu    sync.Mutex
	order []string
	by    map[string]ViewerReport
}

// addViewer records holder's report and applies it if no client shows the
// session.
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

// removeViewer forgets holder's report. What the session answers stays until
// a client or another viewer says otherwise, as it does when a client leaves.
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

// applyViewers answers from the newest viewer's report while no TUI client
// shows the session. Graphics are offered when any viewer's terminal draws
// them.
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

// noteTUIClients records how many TUI clients show the session. Every pane
// is told whether none does, which is when a viewer's fit lease sizes it, and
// with none the viewers' report applies.
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
