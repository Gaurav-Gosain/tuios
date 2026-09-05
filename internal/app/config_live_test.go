package app

import (
	"strconv"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// A setting the user changed has to be in force on the next frame, whichever of
// the two ways in it came: a row on the settings page, or the config file
// itself edited in another pane.

// liveOS is a client with a config, ready to take a settings change or a
// reload.
func liveOS(t *testing.T) *OS {
	t.Helper()
	win := newTestWindow(t, "config-live", 60, 12)
	m := newTestOS(win)
	m.Width, m.Height = 90, 30
	m.UserConfig = config.DefaultConfig()
	m.Settings = config.DefaultSettings()
	return m
}

// TestSettingsRowsApplyLive is the maintainer's second question, asked of a
// sample rather than of one option.
//
// A row writes through setOption, which validates against the registry, writes
// the config and pushes it onto the settings the renderer reads. Nothing on
// this path waits for a save or a restart, and these are the reads that prove
// it for the three kinds of option the page offers.
func TestSettingsRowsApplyLive(t *testing.T) {
	cases := []struct {
		path, value string
		// read is what the running client answers after the write. It reads the
		// place the renderer reads, not the config the row wrote, or it would
		// only be testing that a struct field can be assigned.
		read func(m *OS) string
	}{
		{"spotlight.dim", "37", func(m *OS) string {
			return strconv.Itoa(m.spotlightConfig().DimPercent())
		}},
		{"spotlight.radius", "23", func(m *OS) string {
			return strconv.Itoa(m.spotlightConfig().RadiusRows())
		}},
		{"spotlight.edge", config.SpotlightEdgeSoft, func(m *OS) string {
			return m.spotlightConfig().EdgeStyle()
		}},
		{"appearance.border_style", "double", func(m *OS) string {
			return m.Settings.BorderStyle
		}},
		{"appearance.dockbar_position", "top", func(m *OS) string {
			return m.Settings.DockbarPosition
		}},
		{"appearance.sidebar.width", "31", func(m *OS) string {
			return strconv.Itoa(m.Settings.SidebarWidth)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			m := liveOS(t)
			before := tc.read(m)
			if before == tc.value {
				t.Fatalf("%s already reads %q, so this cannot show that the row changed it",
					tc.path, tc.value)
			}
			m.setOption(tc.path, tc.value)
			if got := tc.read(m); got != tc.value {
				t.Errorf("%s reads %q after the row set it to %q; the change needs a restart",
					tc.path, got, tc.value)
			}
		})
	}
}

// TestSpotlightDimReachesThePassLive is the report itself, driven end to end
// through the row rather than through the config struct.
//
// The two settings have to draw different frames, with a theme and without one.
// Untheme they did not: the pass took a branch that put the whole screen on SGR
// 2 and read the dim setting nowhere.
func TestSpotlightDimReachesThePassLive(t *testing.T) {
	for _, themeID := range []string{"", "catppuccin_mocha"} {
		name := "no theme"
		if themeID != "" {
			name = themeID
		}
		t.Run(name, func(t *testing.T) {
			withTheme(t, themeID)
			m := liveOS(t)
			m.spotlight.on = true

			light := map[int]float64{}
			for _, dim := range []int{config.SpotlightMinDim, config.SpotlightMaxDim} {
				m.setOption("spotlight.dim", strconv.Itoa(dim))
				// A fresh beam each time, so the reading is the setting's and
				// not the last pass's output dimmed again.
				m.spotlight = spotlightState{on: true}
				canvas := spotlightTestCanvas(t, 80, 24)
				m.applySpotlight(canvas)
				light[dim] = spotlightBrightness(t, cellStyleAt(canvas, 2, 2).Fg)
			}
			lo, hi := light[config.SpotlightMinDim], light[config.SpotlightMaxDim]
			if hi >= lo {
				t.Errorf("dim %d left the cell at %.3f and dim %d at %.3f; the setting does nothing",
					config.SpotlightMinDim, lo, config.SpotlightMaxDim, hi)
			}
		})
	}
}

// TestConfigReloadPutsEverySectionInForce. The reload path used to land the
// [appearance] section on this session's settings and stop there, so a
// [spotlight] edited in the file was read from a config nothing had replaced.
func TestConfigReloadPutsEverySectionInForce(t *testing.T) {
	m := liveOS(t)
	if m.spotlightConfig().DimPercent() == 44 {
		t.Fatal("the client already reads dim 44, so this cannot show the reload landed")
	}

	next := config.DefaultConfig()
	next.Spotlight.Dim = 44
	next.Spotlight.Radius = 17
	next.Appearance.BorderStyle = "double"
	m.Update(ConfigReloadedMsg{Config: next})

	if got := m.spotlightConfig().DimPercent(); got != 44 {
		t.Errorf("the reload left the beam at dim %d, want 44", got)
	}
	if got := m.spotlightConfig().RadiusRows(); got != 17 {
		t.Errorf("the reload left the beam at radius %d, want 17", got)
	}
	if got := m.Settings.BorderStyle; got != "double" {
		t.Errorf("the reload left the border style at %q, want double", got)
	}
}

// TestConfigReloadPutsTheBeamWhereTheFileSaysIt. The beam is client-local, so
// nothing else on the reload path carries it. Without this the screen and the
// config disagree about whether it is drawn, and the next toggle writes the
// disagreement back to the file.
func TestConfigReloadPutsTheBeamWhereTheFileSaysIt(t *testing.T) {
	m := liveOS(t)
	m.spotlight.on = false

	on := config.DefaultConfig()
	on.Spotlight.Enabled = boolPtr(true)
	m.Update(ConfigReloadedMsg{Config: on})
	if !m.SpotlightOn() {
		t.Error("a file that turned the beam on left it off")
	}

	off := config.DefaultConfig()
	off.Spotlight.Enabled = boolPtr(false)
	m.Update(ConfigReloadedMsg{Config: off})
	if m.SpotlightOn() {
		t.Error("a file that turned the beam off left it on")
	}
}

// TestConfigReloadFailureKeepsWhatIsRunning. A file that does not parse must
// leave the session exactly as it is and say so. Rendering the defaults over a
// running session every time somebody saved a typo would be far worse than
// waiting for the next save.
func TestConfigReloadFailureKeepsWhatIsRunning(t *testing.T) {
	m := liveOS(t)
	m.setOption("spotlight.dim", "44")
	m.setOption("appearance.border_style", "double")

	m.Update(ConfigReloadFailedMsg{Err: errBrokenConfig{}})

	if got := m.spotlightConfig().DimPercent(); got != 44 {
		t.Errorf("a broken file moved the beam to dim %d; it was at 44", got)
	}
	if got := m.Settings.BorderStyle; got != "double" {
		t.Errorf("a broken file moved the border style to %q; it was double", got)
	}
	if len(m.Notifications) == 0 {
		t.Fatal("a broken config file said nothing on screen")
	}
	found := false
	for _, n := range m.Notifications {
		if n.Type == "error" {
			found = true
		}
	}
	if !found {
		t.Errorf("a broken config file raised no error notification: %+v", m.Notifications)
	}
}

// TestConfigReloadWithNoConfigChangesNothing. The watcher delivers exactly one
// of a config and an error, but a message with neither must not panic a client.
func TestConfigReloadWithNoConfigChangesNothing(t *testing.T) {
	m := liveOS(t)
	before := m.Settings.BorderStyle
	var cmd tea.Cmd
	_, cmd = m.Update(ConfigReloadedMsg{})
	if cmd != nil {
		t.Error("an empty reload produced a command")
	}
	if m.Settings.BorderStyle != before {
		t.Errorf("an empty reload moved the border style to %q", m.Settings.BorderStyle)
	}
}

type errBrokenConfig struct{}

func (errBrokenConfig) Error() string { return "[appearance] theme: unbalanced quote" }
