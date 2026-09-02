package app

// A pane the user has scrolled back must stay on the line they stopped at, and
// the end of its history moves without them.
//
// The rule and the reasoning live on the window, in
// internal/terminal/window_scroll_anchor.go. These two sweeps are only where
// the rule is asked, once per message in OS.Update, so no handler has to
// remember it. Both are a field read per window in the ordinary case, where no
// pane is scrolled at all, so they cost nothing on the idle path that
// BenchmarkIdleTick defends.

// applyScrollAnchors puts every scrolled pane back on its anchored line.
func (m *OS) applyScrollAnchors() {
	for _, w := range m.Windows {
		w.ApplyScrollAnchor()
	}
}

// recordScrollAnchors takes a scroll the user just made and remembers where in
// the history it left each pane.
func (m *OS) recordScrollAnchors() {
	for _, w := range m.Windows {
		w.RecordScrollAnchor()
	}
}
