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
	prev := config.ScrollColumnWidth
	t.Cleanup(func() { config.ScrollColumnWidth = prev })

	config.ScrollColumnWidth = 40
	host := modeOS(t, LayoutModeScrolling, false, 0, 3, 160, 48)
	host.ScrollColumnWidth = 40
	host.TileAllWindows()
	host.CompleteAllAnimations()

	state := host.BuildSessionState()
	if state.PaneGeometry == nil || state.PaneGeometry.ScrollColumnWidth != 40 {
		t.Fatalf("the session state must carry the column width; got %+v", state.PaneGeometry)
	}

	// A second client whose own process was configured for a wider column.
	config.ScrollColumnWidth = 70
	joiner := modeOS(t, LayoutModeScrolling, false, 0, 3, 160, 48)
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
	prev := config.ScrollColumnWidth
	t.Cleanup(func() { config.ScrollColumnWidth = prev })

	config.ScrollColumnWidth = 70
	m := modeOS(t, LayoutModeScrolling, false, 0, 2, 160, 48)
	m.ScrollColumnWidth = 70

	old := &session.SessionState{PaneGeometry: &session.PaneGeometryState{}}
	m.adoptPaneGeometry(old)
	if m.ScrollColumnWidth != 70 {
		t.Errorf("column width is %d after adopting a state that never mentioned it, want 70", m.ScrollColumnWidth)
	}
}

// The column width reaches the strip. A setting nothing reads is the failure
// this whole feature is a fix for: appearance.gap existed in the scrolling
// layout as a field and nothing ever set it.
func TestScrollColumnWidthDecidesTheColumn(t *testing.T) {
	prev := config.ScrollColumnWidth
	t.Cleanup(func() { config.ScrollColumnWidth = prev })

	for _, pct := range []int{30, 55, 90} {
		config.ScrollColumnWidth = pct
		m := modeOS(t, LayoutModeScrolling, false, 0, 3, 200, 48)
		m.ScrollColumnWidth = pct
		m.TileAllWindows()
		m.CompleteAllAnimations()
		want := m.ScrollingViewWidth() * pct / 100
		if got := m.Windows[0].Width; got != want {
			t.Errorf("at %d%% a column is %d columns of %d, want %d", pct, got, m.ScrollingViewWidth(), want)
		}
	}
}

// The master ratio was already session state and already moved by the resize
// keys; what it had no way to be was a preference. The setting seeds it and the
// settings row reads the session's value back, so a row cannot show a number
// the layout is not using.
func TestMasterRatioSettingMovesTheSplit(t *testing.T) {
	prev := config.MasterRatioPercent
	t.Cleanup(func() { config.MasterRatioPercent = prev })

	m := modeOS(t, LayoutModeMasterStack, false, 0, 2, 160, 40)
	m.UserConfig = config.DefaultConfig()
	m.ConfigReadOnly = true

	m.SetMasterRatioSetting(30)
	if got := m.MasterRatioPercent(); got != 30 {
		t.Fatalf("the row reads %d%% after being set to 30%%", got)
	}
	m.CompleteAllAnimations()
	narrow := m.Windows[0].Width

	m.SetMasterRatioSetting(70)
	if got := m.MasterRatioPercent(); got != 70 {
		t.Fatalf("the row reads %d%% after being set to 70%%", got)
	}
	m.CompleteAllAnimations()
	wide := m.Windows[0].Width

	if wide <= narrow {
		t.Errorf("the master pane is %d columns at 70%% and %d at 30%%: the setting does not reach the tiler", wide, narrow)
	}
	if got := m.UserConfig.Appearance.MasterRatio; got != 70 {
		t.Errorf("the config kept %d, so the preference would not survive a restart", got)
	}
}

// The row must not report a value the layout is not using. The resize keys move
// the ratio underneath it, which is why it reads the model rather than the
// config, and why it rounds rather than truncating: 0.55 is 55%, not 54%.
func TestMasterRatioRowFollowsTheResizeKeys(t *testing.T) {
	m := modeOS(t, LayoutModeMasterStack, false, 0, 2, 160, 40)
	m.MasterRatio = 0.5
	m.ResizeMasterWidth(0.05)
	if got := m.MasterRatioPercent(); got != 55 {
		t.Errorf("the row reads %d%% after the resize key took the ratio to %.2f", got, m.MasterRatio)
	}
}

// startup.layout picks the scheme a fresh session tiles with. It is applied
// before tiling is switched on, because switching on builds the layout the
// current mode asks for: choosing the mode afterwards would build a BSP tree
// and immediately throw it away.
func TestStartupLayoutChoosesTheMode(t *testing.T) {
	for _, mode := range []string{LayoutModeBSP, LayoutModeMasterStack, LayoutModeScrolling} {
		t.Run(mode, func(t *testing.T) {
			m := modeOS(t, LayoutModeBSP, false, 0, 2, 120, 40)
			m.AutoTiling = false
			m.UseScrollingLayout, m.UseBSPLayout = false, true
			m.UserConfig = config.DefaultConfig()
			m.UserConfig.Startup.Tiled = true
			m.UserConfig.Startup.Layout = mode

			m.applyStartupTiling()

			if !m.AutoTiling {
				t.Fatal("startup.tiled must still turn tiling on")
			}
			if got := m.LayoutModeName(); got != mode {
				t.Errorf("a session started with startup.layout = %q is in %q", mode, got)
			}
		})
	}
}

// An unset or unknown startup.layout leaves the mode alone, which is BSP. A
// config written before the setting existed must not be read as a request for
// something else.
func TestAnUnsetStartupLayoutKeepsTheDefault(t *testing.T) {
	for _, name := range []string{"", "tabbed"} {
		m := modeOS(t, LayoutModeBSP, false, 0, 2, 120, 40)
		m.AutoTiling = false
		m.UseScrollingLayout, m.UseBSPLayout = false, true
		m.UserConfig = config.DefaultConfig()
		m.UserConfig.Startup.Tiled = true
		m.UserConfig.Startup.Layout = name

		m.applyStartupTiling()
		if got := m.LayoutModeName(); got != LayoutModeBSP {
			t.Errorf("startup.layout = %q left the session in %q, want the default", name, got)
		}
	}
}

// A workspace nobody has tiled yet has no remembered master ratio, and what it
// falls back to has to be the ratio in force rather than a literal half.
//
// NEGATIVE CONTROL: with the fallback back at 0.5, switching to a workspace for
// the first time takes a 70% split to 50% and leaves it there - the setting
// reads as ignored, and a ratio the resize keys had moved is thrown away.
func TestAFreshWorkspaceStartsAtTheConfiguredMasterRatio(t *testing.T) {
	prev := config.MasterRatioPercent
	t.Cleanup(func() { config.MasterRatioPercent = prev })
	config.MasterRatioPercent = 70

	m := modeOS(t, LayoutModeMasterStack, false, 0, 4, 160, 40)
	m.MasterRatio = 0.7
	m.Windows[2].Workspace = 2
	m.Windows[3].Workspace = 2
	m.TileAllWindows()

	m.SwitchToWorkspace(2)
	if got := m.MasterRatioPercent(); got != 70 {
		t.Errorf("the first visit to workspace 2 put the master ratio at %d%%, want the configured 70%%", got)
	}

	// A workspace that was left at its own ratio keeps it, which is the half of
	// the behaviour the fallback must not trample.
	m.ResizeMasterWidth(-0.2)
	m.SwitchToWorkspace(1)
	m.SwitchToWorkspace(2)
	if got := m.MasterRatioPercent(); got != 50 {
		t.Errorf("workspace 2 came back at %d%%, want the 50%% it was left at", got)
	}
}
