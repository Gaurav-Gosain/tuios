package app

// A layout update is not one resize; it is several. Settle the border
// allowance, place the rectangle, reclaim the columns a divider was holding,
// drain a resize the pointer deferred - each step resizes the pane, and each
// resize used to reach the guest as its own SIGWINCH. A full-screen program
// repaints on every one, so a layout-mode switch that left a pane exactly the
// size it started at still cost it two full repaints, and a shell left two
// stacked prompts behind.
//
// Only the size a pane settles at was ever real. settleSizes holds the
// announcements for the duration of an update and sends each pane the one size
// it ended up with, or nothing at all when it ended up where it started.

// settleSizes runs a layout update with every pane's announcement held, then
// tells each pane its settled size once.
//
// Nesting is expected - a mode switch retiles, a retile places panes - so the
// hold is depth counted and only the outermost call releases.
func (m *OS) settleSizes(apply func()) {
	m.announceDepth++
	if m.announceDepth == 1 {
		for _, w := range m.Windows {
			if w != nil {
				w.HoldAnnouncements()
			}
		}
	}
	defer func() {
		m.announceDepth--
		if m.announceDepth > 0 {
			return
		}
		// Over m.Windows as it stands now, not as it was: a pane closed inside
		// the update is gone and a pane opened inside it was never held, and
		// releasing a pane that is not holding does nothing.
		for _, w := range m.Windows {
			if w != nil {
				w.ReleaseAnnouncements()
			}
		}
	}()
	apply()
}
