package app

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The report: "if i switched between sessions, the window size if its full
// width or smth in scrolling mode, it reset the width to 50%".
//
// A session switch throws away everything this client holds for the session it
// is leaving and rebuilds from the state the daemon keeps. The BSP layout
// survives that, because its trees are in the state. The scrolling layout's
// columns were not: the state carried only how far the strip was scrolled, so
// the columns came back one per pane at the default width. A column widened
// with the width key lost its width, and a column holding two stacked panes
// came back as two columns.

// scrollRig is a real daemon session in the scrolling layout with n panes.
func scrollRig(t *testing.T, n int) *rig {
	t.Helper()
	prev := config.Global.AnimationsEnabled
	config.Global.AnimationsEnabled = false
	t.Cleanup(func() { config.Global.AnimationsEnabled = prev })

	r := newRig(t, n)
	r.m.AutoTiling = true
	r.m.ApplyLayoutModeName(config.LayoutModeScrolling)
	r.m.TileAllWindows()
	r.m.SyncStateToDaemon()
	return r
}

// roundTrip switches to another session and back, which is the gesture from the
// report.
func roundTrip(t *testing.T, r *rig) {
	t.Helper()
	home := r.m.SessionName
	if err := r.m.SwitchToSession(home + "-elsewhere"); err != nil {
		t.Fatalf("switch away: %v", err)
	}
	if err := r.m.SwitchToSession(home); err != nil {
		t.Fatalf("switch back: %v", err)
	}
	if r.m.SessionName != home {
		t.Fatalf("the round trip ended on %q, want %q", r.m.SessionName, home)
	}
}

// TestAColumnKeepsItsWidthAcrossASessionSwitch is the report.
func TestAColumnKeepsItsWidthAcrossASessionSwitch(t *testing.T) {
	r := scrollRig(t, 3)
	viewW := r.m.ScrollingViewWidth()

	sl := r.m.GetOrCreateScrollingLayout()
	col := sl.FocusedCol
	before := sl.ResolveColumnWidth(col, viewW)
	// The width key until the column is at the widest preset, which is what
	// "full width" is on the strip.
	for range len(sl.PresetWidths) {
		r.m.ScrollingCycleWidth()
		if sl.Columns[col].Proportion >= 0.9 {
			break
		}
	}
	widened := sl.ResolveColumnWidth(col, viewW)
	if widened <= before {
		t.Fatalf("setup: the width key left the column at %d cells (was %d)", widened, before)
	}
	r.m.SyncStateToDaemon()

	roundTrip(t, r)

	after := r.m.GetOrCreateScrollingLayout()
	if len(after.Columns) <= col {
		t.Fatalf("the strip came back with %d columns, and the widened one was number %d", len(after.Columns), col)
	}
	if got := after.ResolveColumnWidth(col, r.m.ScrollingViewWidth()); got != widened {
		t.Errorf("the column came back %d cells wide, want the %d it was widened to", got, widened)
	}
}

// TestAStackedColumnStaysStackedAcrossASessionSwitch is the same loss on the
// other thing a column holds: which panes are in it.
func TestAStackedColumnStaysStackedAcrossASessionSwitch(t *testing.T) {
	r := scrollRig(t, 3)
	sl := r.m.GetOrCreateScrollingLayout()
	before := len(sl.Columns)

	sl.FocusedCol = 0
	r.m.ScrollingConsumeWindow()
	stacked := len(sl.Columns)
	if stacked != before-1 {
		t.Fatalf("setup: consuming a window left %d columns (was %d)", stacked, before)
	}
	r.m.SyncStateToDaemon()

	roundTrip(t, r)

	if got := len(r.m.GetOrCreateScrollingLayout().Columns); got != stacked {
		t.Errorf("the strip came back with %d columns, want the %d it had with two panes stacked", got, stacked)
	}
}

// TestAPeerTakesAWidenedColumn is the same state on the other route it travels:
// a second client on the session. Before the columns were in the state, the
// peer kept its own widths, so two clients on one session drew the strip
// differently.
func TestAPeerTakesAWidenedColumn(t *testing.T) {
	r := scrollRig(t, 3)
	p := joinPeerOS(t, r, r.cols, r.rows)
	p.m.AutoTiling = true
	p.m.ApplyLayoutModeName(config.LayoutModeScrolling)
	p.m.TileAllWindows()

	ex := &exchange{t: t}
	ex.route(r.client, r.m, "local")
	ex.route(p.c, p.m, "peer")
	ex.settle(200, 50*time.Millisecond)

	sl := r.m.GetOrCreateScrollingLayout()
	col := sl.FocusedCol
	for range len(sl.PresetWidths) {
		r.m.ScrollingCycleWidth()
		if sl.Columns[col].Proportion >= 0.9 {
			break
		}
	}
	want := sl.Columns[col].Proportion
	r.m.SyncStateToDaemon()
	ex.settle(200, 100*time.Millisecond)

	got := p.m.GetOrCreateScrollingLayout()
	if len(got.Columns) != len(sl.Columns) {
		t.Fatalf("the peer has %d columns, want %d", len(got.Columns), len(sl.Columns))
	}
	if got.Columns[col].Proportion != want {
		t.Errorf("the peer's column %d is at proportion %v, want the %v the other client widened it to",
			col, got.Columns[col].Proportion, want)
	}
}
