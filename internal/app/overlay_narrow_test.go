package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// narrowScreens are the sizes the overlays have to survive: a tall narrow
// terminal, a short wide one, the narrowest viewport worth supporting, and a
// normal terminal as a control.
var narrowScreens = []struct {
	name string
	w, h int
}{
	{"tall-narrow", 51, 37},
	{"short-wide", 90, 20},
	{"very-narrow", 30, 24},
	{"desktop", 120, 40},
}

// assertFitsScreen fails if any line of out is wider than w or the block is
// taller than h. An overlay wider than the screen has its right-hand side drawn
// off the edge, where it cannot be read or scrolled to.
func assertFitsScreen(t *testing.T, name, out string, w, h int) {
	t.Helper()
	if out == "" {
		return
	}
	lines := strings.Split(out, "\n")
	for i, ln := range lines {
		if lw := lipgloss.Width(ln); lw > w {
			t.Errorf("%s: line %d is %d cells wide, screen is %d: %q", name, i, lw, w, ln)
			return
		}
	}
	if len(lines) > h {
		t.Errorf("%s: %d lines tall, screen is %d", name, len(lines), h)
	}
}

// newNarrowOS builds an OS sized to a given screen with every overlay's state
// populated enough to render.
func newNarrowOS(t *testing.T, w, h int) *OS {
	t.Helper()
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})
	if m.KeybindRegistry == nil {
		m.KeybindRegistry = config.NewKeybindRegistry(config.DefaultConfig())
	}
	m.Width, m.Height = w, h
	m.EffectiveWidth, m.EffectiveHeight = w, h
	return m
}

// TestOverlayPanelsFitNarrowScreens renders every overlay panel at each screen
// size and asserts none of them draws outside the screen.
func TestOverlayPanelsFitNarrowScreens(t *testing.T) {
	for _, sc := range narrowScreens {
		t.Run(sc.name, func(t *testing.T) {
			m := newNarrowOS(t, sc.w, sc.h)

			m.HelpCategory = -1
			cats := GetHelpCategories(m.KeybindRegistry)
			for i := range cats {
				m.HelpCategory = i
				m.HelpScrollOffset = 0
				out, _ := m.RenderHelpMenu()
				assertFitsScreen(t, fmt.Sprintf("help[%s]", cats[i].Name), out, sc.w, sc.h)
			}
			m.HelpSearchMode = true
			m.HelpSearchQuery = "window"
			out, _ := m.RenderHelpMenu()
			assertFitsScreen(t, "help search", out, sc.w, sc.h)
			m.HelpSearchMode = false

			out, _, _ = m.renderCommandPalette()
			assertFitsScreen(t, "palette", out, sc.w, sc.h)

			m.CommandPaletteQuery = "zzzz-no-match"
			out, _, _ = m.renderCommandPalette()
			assertFitsScreen(t, "palette empty", out, sc.w, sc.h)
			m.CommandPaletteQuery = ""

			for i := range m.settingsCategories() {
				m.SettingsCategory = i
				m.SettingsSelected = 0
				out, _, _ = m.renderSettings()
				assertFitsScreen(t, fmt.Sprintf("settings[%d]", i), out, sc.w, sc.h)
			}
			// A long free-text value must not push the row past the panel.
			ci, ii, _, ok := findSetting(m, "Behavior", "Preferred shell")
			if ok {
				m.SettingsCategory, m.SettingsSelected = ci, ii
				m.SettingsEditing = true
				m.SettingsEditBuffer = strings.Repeat("/very/long/path", 8)
				out, _, _ = m.renderSettings()
				assertFitsScreen(t, "settings editing", out, sc.w, sc.h)
				m.SettingsEditing = false
			}

			m.OpenThemePicker()
			out, _, _ = m.renderThemePicker()
			assertFitsScreen(t, "themepicker", out, sc.w, sc.h)
			m.CancelThemePicker()

			// Session switcher outside daemon mode is the informational panel.
			out, _, _ = m.renderSessionSwitcher()
			assertFitsScreen(t, "session (no daemon)", out, sc.w, sc.h)
			m.SessionSwitcherConfirmDelete = "a-very-long-session-name-that-will-not-fit-on-a-narrow-screen"
			out, _, _ = m.renderSessionSwitcher()
			assertFitsScreen(t, "session confirm", out, sc.w, sc.h)
			m.SessionSwitcherConfirmDelete = ""

			m.LayoutPickerItems = []LayoutTemplate{
				{Name: "a-layout-name-that-is-far-too-long-for-a-narrow-screen", AutoTiling: true},
				{Name: "dev"},
			}
			out, _, _ = m.renderLayoutPicker()
			assertFitsScreen(t, "layout picker", out, sc.w, sc.h)
			m.LayoutPickerMode = "save"
			m.LayoutSaveBuffer = strings.Repeat("name", 30)
			out, _, _ = m.renderLayoutPicker()
			assertFitsScreen(t, "layout save", out, sc.w, sc.h)
			m.LayoutPickerMode = ""

			out, _, _ = m.renderAggregateView()
			assertFitsScreen(t, "aggregate", out, sc.w, sc.h)

			quit, _, _ := m.renderQuitConfirmDialog()
			assertFitsScreen(t, "quit", quit, sc.w, sc.h)
		})
	}
}

// TestOverlayLayersFitNarrowScreens turns on every overlay at once and checks
// each placed layer lands wholly inside the screen.
func TestOverlayLayersFitNarrowScreens(t *testing.T) {
	for _, sc := range narrowScreens {
		t.Run(sc.name, func(t *testing.T) {
			m := newNarrowOS(t, sc.w, sc.h)
			m.ShowHelp = true
			m.HelpCategory = -1
			m.ShowCommandPalette = true
			m.ShowSettings = true
			m.ShowSessionSwitcher = true
			m.ShowLayoutPicker = true
			m.ShowQuitConfirm = true
			m.ShowLogs = true
			m.ShowCacheStats = true
			m.ShowTapeManager = true
			m.PrefixActive = true
			m.LastPrefixTime = m.LastPrefixTime.Add(-time.Hour)
			m.LogMessages = []LogMessage{
				{Level: "ERROR", Message: strings.Repeat("a long log message ", 8)},
				{Level: "INFO", Message: "short"},
			}
			m.ShowNotification(strings.Repeat("a long notification message ", 4), "error", time.Minute)
			m.ShowKeys = true
			m.RecentKeys = []KeyEvent{
				{Key: "A", Modifiers: []string{"Ctrl", "Alt", "Shift"}, Timestamp: time.Now(), Count: 3},
				{Key: "B", Timestamp: time.Now(), Count: 1},
				{Key: "Enter", Timestamp: time.Now(), Count: 1},
				{Key: "Backspace", Timestamp: time.Now(), Count: 9},
			}

			for _, layer := range m.renderOverlays() {
				id := layer.GetID()
				if r := layer.GetX() + layer.Width(); r > sc.w {
					t.Errorf("layer %q spans x=%d..%d, screen is %d wide", id, layer.GetX(), r, sc.w)
				}
				if b := layer.GetY() + layer.Height(); b > sc.h {
					t.Errorf("layer %q spans y=%d..%d, screen is %d tall", id, layer.GetY(), b, sc.h)
				}
				if layer.GetX() < 0 || layer.GetY() < 0 {
					t.Errorf("layer %q is placed off the top-left at (%d,%d)", id, layer.GetX(), layer.GetY())
				}
			}
		})
	}
}

// TestOverlayDesktopSizesUnchanged pins the panel sizes at a normal terminal
// size. Fitting the overlays to narrow screens must not shrink them on a screen
// that has the room, which is the shared path native tuios renders on.
func TestOverlayDesktopSizesUnchanged(t *testing.T) {
	m := newNarrowOS(t, 120, 40)

	if got := m.panelWidth(helpPanelInnerWidth); got != helpPanelInnerWidth {
		t.Errorf("help width = %d, want %d", got, helpPanelInnerWidth)
	}
	if w, rows := m.paletteLayout(); w != paletteInnerWidth || rows != paletteMaxVisible {
		t.Errorf("palette layout = %d x %d, want %d x %d", w, rows, paletteInnerWidth, paletteMaxVisible)
	}
	if w, rows := m.themePickerLayout(); w != themePickerInnerWidth || rows != themePickerVisibleRows {
		t.Errorf("theme picker layout = %d x %d, want %d x %d", w, rows, themePickerInnerWidth, themePickerVisibleRows)
	}
	if got := m.dialogWidth(80); got != 80 {
		t.Errorf("log viewer width = %d, want 80", got)
	}
	if got := m.tapeReviewRows(); got != tapeReviewViewportRows {
		t.Errorf("tape review rows = %d, want %d", got, tapeReviewViewportRows)
	}

	cats := m.settingsCategories()
	for i, cat := range cats {
		m.SettingsCategory = i
		w, rows := m.settingsLayout([]string{"Appearance", "Dock", "Behavior"}, len(cat.Items))
		if w != settingsInnerWidth {
			t.Errorf("settings[%s] width = %d, want %d", cat.Name, w, settingsInnerWidth)
		}
		// Every category shows all of its rows at a desktop height: nothing
		// scrolls that did not scroll before.
		if rows < len(cat.Items) || rows < settingsVisibleRows {
			t.Errorf("settings[%s] rows = %d, want at least %d", cat.Name, rows, max(len(cat.Items), settingsVisibleRows))
		}
	}

	// The help panel is 74 + 4 cells across and its tab strip stays on one row.
	out, geo := m.RenderHelpMenu()
	if geo.Width != helpPanelInnerWidth+4 {
		t.Errorf("help panel width = %d, want %d", geo.Width, helpPanelInnerWidth+4)
	}
	if lipgloss.Height(out) != 25 {
		t.Errorf("help panel height = %d, want 25 (one tab row)", lipgloss.Height(out))
	}
	for _, r := range geo.Tabs {
		if r.Y0 != geo.Tabs[0].Y0 {
			t.Errorf("help tabs wrapped onto %d rows at desktop width", r.Y0-geo.Tabs[0].Y0+1)
			break
		}
	}
}

// TestWhichKeyFitsNarrowScreens renders the which-key overlay for every prefix
// at every position and checks it lands wholly on screen.
func TestWhichKeyFitsNarrowScreens(t *testing.T) {
	prefixes := []struct {
		name  string
		apply func(*OS)
	}{
		{"leader", func(m *OS) {}},
		{"workspace", func(m *OS) { m.WorkspacePrefixActive = true }},
		{"minimize", func(m *OS) { m.MinimizePrefixActive = true }},
		{"window", func(m *OS) { m.TilingPrefixActive = true }},
		{"debug", func(m *OS) { m.DebugPrefixActive = true }},
		{"tape", func(m *OS) { m.TapePrefixActive = true }},
		{"layout", func(m *OS) { m.LayoutPrefixActive = true }},
	}
	positions := []string{"top-left", "top-right", "bottom-left", "bottom-right", "center"}

	oldPos := config.WhichKeyPosition
	defer func() { config.WhichKeyPosition = oldPos }()

	for _, sc := range narrowScreens {
		for _, p := range prefixes {
			for _, pos := range positions {
				t.Run(sc.name+"/"+p.name+"/"+pos, func(t *testing.T) {
					config.WhichKeyPosition = pos
					m := newNarrowOS(t, sc.w, sc.h)
					m.PrefixActive = true
					m.LastPrefixTime = time.Now().Add(-time.Hour)
					p.apply(m)

					found := false
					for _, layer := range m.renderOverlays() {
						if layer.GetID() != "whichkey" {
							continue
						}
						found = true
						assertFitsScreen(t, "whichkey", layer.GetContent(), sc.w, sc.h)
						if layer.GetX() < 0 || layer.GetY() < 0 ||
							layer.GetX()+layer.Width() > sc.w || layer.GetY()+layer.Height() > sc.h {
							t.Errorf("whichkey placed at (%d,%d) size %dx%d, screen is %dx%d",
								layer.GetX(), layer.GetY(), layer.Width(), layer.Height(), sc.w, sc.h)
						}
					}
					if !found {
						t.Fatal("no whichkey layer rendered")
					}
				})
			}
		}
	}
}

// TestTapeDialogsFitNarrowScreens renders the tape manager and the project-tape
// review dialog, both of which size themselves from their own content.
func TestTapeDialogsFitNarrowScreens(t *testing.T) {
	for _, sc := range narrowScreens {
		t.Run(sc.name, func(t *testing.T) {
			m := newNarrowOS(t, sc.w, sc.h)

			m.InitTapeManager()
			m.TapeManager.Files = []TapeFile{
				{Name: "a-tape-file-name-that-is-much-too-long-to-fit", Size: 4096, Modified: time.Now()},
				{Name: "short", Size: 12, Modified: time.Now()},
			}
			assertFitsScreen(t, "tape manager", m.RenderTapeManager(), sc.w, sc.h)

			m.TapeManager.Mode = TapeManagerNaming
			m.TapeManager.NameBuffer = strings.Repeat("name", 30)
			assertFitsScreen(t, "tape manager naming", m.RenderTapeManager(), sc.w, sc.h)

			m.TapeManager.Mode = TapeManagerConfirmDelete
			assertFitsScreen(t, "tape manager delete", m.RenderTapeManager(), sc.w, sc.h)

			m.TapeReview = &TapeReviewState{
				Path:    "/home/someone/very/deep/project/directory/tree/.tuios.tape",
				Dir:     "/home/someone/very/deep/project/directory/tree",
				Content: []byte(strings.Repeat("Type \"a command line that is quite long indeed\"\n", 40)),
			}
			assertFitsScreen(t, "tape review", m.RenderTapeReview(), sc.w, sc.h)
		})
	}
}

// TestDockFitsNarrowScreens checks the status bar, which spans the screen and
// has a fixed-width estimate for its right-hand block.
func TestDockFitsNarrowScreens(t *testing.T) {
	for _, sc := range narrowScreens {
		t.Run(sc.name, func(t *testing.T) {
			m := newNarrowOS(t, sc.w, sc.h)
			dock, _ := m.renderDockString()
			assertFitsScreen(t, "dock", dock, sc.w, sc.h)
		})
	}
}

// TestWelcomeSplashFitsNarrowScreens renders the no-windows splash, which is
// the first thing anyone sees.
func TestWelcomeSplashFitsNarrowScreens(t *testing.T) {
	for _, sc := range narrowScreens {
		t.Run(sc.name, func(t *testing.T) {
			m := newNarrowOS(t, sc.w, sc.h)
			for _, layer := range m.renderOverlays() {
				if layer.GetID() != "welcome" {
					continue
				}
				assertFitsScreen(t, "welcome", layer.GetContent(), sc.w, sc.h)
				return
			}
			t.Fatal("no welcome layer rendered")
		})
	}
}
