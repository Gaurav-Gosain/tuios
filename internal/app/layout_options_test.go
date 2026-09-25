package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The three layout settings this file covers are the ones that decide how many
// cells a pane gets, so each of them is either seeded from the config and then
// settled by the session, or it is a bug waiting for a second client. A PTY has
// exactly one size; see the note at the top of pane_geometry.go.

// TestScrollColumnWidthIsSettledAcrossTheSession is the multi-client rule for
// the newest geometry input. Two clients whose config files disagree about a
// column's width would resolve every column to a different number of cells and
// drag the shared PTYs between the two answers, which is the failure shared
// borders and the pane gap were moved into session state to stop.
//
// NEGATIVE CONTROL: with the ScrollColumnWidth clause removed from
// adoptPaneGeometry, the joining client keeps its own 70% and lays the same
// three panes out 32 columns wider than the client it joined.
func TestScrollColumnWidthIsSettledAcrossTheSession(t *testing.T) {
	prev := config.Global.ScrollColumnWidth
	t.Cleanup(func() { config.Global.ScrollColumnWidth = prev })

	config.Global.ScrollColumnWidth = 40
	host := modeOS(t, LayoutModeScrolling, false, 0, 3, 160, 48)
	host.ScrollColumnWidth = 40
	host.TileAllWindows()
	host.CompleteAllAnimations()

	state := host.BuildSessionState()
	if state.PaneGeometry == nil || state.PaneGeometry.ScrollColumnWidth != 40 {
		t.Fatalf("the session state must carry the column width; got %+v", state.PaneGeometry)
	}

	// A second client whose own process was configured for a wider column.
	config.Global.ScrollColumnWidth = 70
	joiner := modeOS(t, LayoutModeScrolling, false, 0, 3, 160, 48)
	host.Settings = config.Global
	joiner.ScrollColumnWidth = 70
	joiner.TileAllWindows()
	joiner.CompleteAllAnimations()

	ownWidth := joiner.Windows[0].Width
	hostWidth := host.Windows[0].Width
	if ownWidth == hostWidth {
		t.Fatalf("the two configured widths produce the same column (%d), so this proves nothing", ownWidth)
	}

	if !joiner.adoptPaneGeometry(state) {
		t.Fatal("adopting a session whose column width differs must report a change, so the caller retiles")
	}
	joiner.TileAllWindows()
	joiner.CompleteAllAnimations()

	if joiner.ScrollColumnWidth != 40 {
		t.Errorf("the joining client holds %d%%, want the session's 40%%", joiner.ScrollColumnWidth)
	}
	for i := range joiner.Windows {
		if got, want := joiner.Windows[i].Width, host.Windows[i].Width; got != want {
			t.Errorf("pane %d is %d columns on the joining client and %d on the host", i, got, want)
		}
	}
}

// A peer that has not said leaves this client on its own configured width. That
// is what makes the field additive: a client too old to send it, or state
// written before it existed, must not reset anybody's layout to a zero.
func TestAnUnsaidColumnWidthLeavesThisClientAlone(t *testing.T) {
	prev := config.Global.ScrollColumnWidth
	t.Cleanup(func() { config.Global.ScrollColumnWidth = prev })

	config.Global.ScrollColumnWidth = 70
	m := modeOS(t, LayoutModeScrolling, false, 0, 2, 160, 48)
	m.ScrollColumnWidth = 70

	old := &session.SessionState{PaneGeometry: &session.PaneGeometryState{}}
	m.adoptPaneGeometry(old)
	if m.ScrollColumnWidth != 70 {
		t.Errorf("column width is %d after adopting a state that never mentioned it, want 70", m.ScrollColumnWidth)
	}
}
