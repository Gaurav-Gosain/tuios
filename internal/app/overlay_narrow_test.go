package app

import (
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/charmbracelet/x/ansi"
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
	{"very-short", 100, 12},
	{"desktop", 120, 40},
	// The accent picker's wide layout: the first screen that gets it, one just
	// over it, and a wide screen too short to keep everything.
	{"wide-picker-floor", 73, 30},
	{"wide-picker", 74, 20},
	{"wide-picker-short", 100, 14},
}

// assertFitsScreen fails if any line of out is wider than w, the block is
// taller than h, or any line pads itself with a control character. An overlay
// wider than the screen has its right-hand side drawn off the edge, where it
// cannot be read or scrolled to.
//
// The control-character half guards a different way of getting the same answer
// wrong. Every row here is padded to the panel width with literal spaces, and
// the fitting arithmetic in overlay_fit.go measures those rows to decide what
// else fits. A tab would break that: it is one byte that lipgloss measures as
// one cell but a terminal advances to its next tab stop, so the panel's own
// idea of where a row ends would stop matching the screen's. Carriage returns
// and the rest are the same failure with a different glyph.
func assertFitsScreen(t *testing.T, name, out string, w, h int) {
	t.Helper()
	if out == "" {
		return
	}
	lines := strings.Split(out, "\n")
	for i, ln := range lines {
		if j := strings.IndexAny(ln, "\t\r\v\f"); j >= 0 {
			t.Errorf("%s: line %d pads with a control character (%q at byte %d): %q",
				name, i, ln[j], j, ln)
			return
		}
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
			m.OpenGotoPicker()
			m.ShowLayoutPicker = true
			m.OpenQuitMenu()
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

// TestWhichKeyFitsNarrowScreens renders the which-key overlay for every prefix
// at every position and checks it lands wholly on screen.
func TestWhichKeyFitsNarrowScreens(t *testing.T) {
	prefixes := []struct {
		name  string
		apply func(*OS)
	}{
		{"leader", func(m *OS) {}},
		{"workspace", func(m *OS) { m.SubMenu = SubMenuWorkspace }},
		{"minimize", func(m *OS) { m.SubMenu = SubMenuMinimize }},
		{"window", func(m *OS) { m.SubMenu = SubMenuTiling }},
		{"debug", func(m *OS) { m.SubMenu = SubMenuDebug }},
		{"tape", func(m *OS) { m.SubMenu = SubMenuTape }},
		{"layout", func(m *OS) { m.SubMenu = SubMenuLayout }},
	}
	positions := []string{"top-left", "top-right", "bottom-left", "bottom-right", "center"}

	oldPos := config.Global.WhichKeyPosition
	defer func() { config.Global.WhichKeyPosition = oldPos }()

	for _, sc := range narrowScreens {
		for _, p := range prefixes {
			for _, pos := range positions {
				t.Run(sc.name+"/"+p.name+"/"+pos, func(t *testing.T) {
					config.Global.WhichKeyPosition = pos
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

// TestTapeReviewFitsNarrowScreens holds the tape review panel to the narrow
// screens. The tape manager and its naming and delete states are checked on
// screen by TestPanelsFitTheScreen; the review opens on a tape found in a
// project folder, which the screen fixture does not set up yet.
func TestTapeReviewFitsNarrowScreens(t *testing.T) {
	for _, sc := range narrowScreens {
		t.Run(sc.name, func(t *testing.T) {
			m := newNarrowOS(t, sc.w, sc.h)
			m.TapeReview = &TapeReviewState{
				Path:    "/home/someone/very/deep/project/directory/tree/.tuios.tape",
				Dir:     "/home/someone/very/deep/project/directory/tree",
				Content: []byte(strings.Repeat("Type \"a command line that is quite long indeed\"\n", 40)),
			}
			assertFitsScreen(t, "tape review", m.RenderTapeReview(), sc.w, sc.h)
		})
	}
}

// tinyScreens are sizes a shrunk client reaches: a narrowed browser tab, a
// pane-sized client, and the least a terminal can be. All but 40x10 leave the
// which-key panel no room for a single binding, and the narrow ones are
// narrower than its "+N more" count.
var tinyScreens = []struct {
	name string
	w, h int
}{
	{"20x4", 20, 4},
	{"35x6", 35, 6},
	{"40x10", 40, 10},
	{"1x1", 1, 1},
	{"80x3", 80, 3},
	{"12x40", 12, 40},
	{"10x6", 10, 6},
	{"5x1", 5, 1},
}

// TestWhichKeyOnTinyScreens holds the leader menus to tiny screens. With no
// room for a binding, clipWhichKey keeps no column and counts every binding
// as more, and the "+N more" line looked for the last column: cols[-1]. A
// client at 35x6 crashed on ctrl+b. The menu now shows the count alone, every
// line as wide as the panel, and the whole frame draws with it.
func TestWhichKeyOnTinyScreens(t *testing.T) {
	prefixes := []struct {
		name  string
		apply func(*OS)
	}{
		{"leader", func(m *OS) {}},
		{"workspace", func(m *OS) { m.SubMenu = SubMenuWorkspace }},
		{"window", func(m *OS) { m.SubMenu = SubMenuTiling }},
		{"layout", func(m *OS) { m.SubMenu = SubMenuLayout }},
	}
	for _, sc := range tinyScreens {
		for _, p := range prefixes {
			t.Run(sc.name+"/"+p.name, func(t *testing.T) {
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
					// Under the title's rule, a line of dashes alone (a frame's
					// edge has corners), there is a binding or the count of the
					// ones that did not fit.
					lines := strings.Split(ansi.Strip(layer.GetContent()), "\n")
					rule := slices.IndexFunc(lines, func(l string) bool {
						l = strings.TrimSpace(l)
						return l != "" && strings.Trim(l, "─") == ""
					})
					if rule < 0 || !slices.ContainsFunc(lines[rule+1:], func(l string) bool { return strings.TrimSpace(l) != "" }) {
						t.Errorf("the menu shows no binding and no count:\n%s", layer.GetContent())
					}
					// Every line is the panel's width: a count wider than a
					// narrow panel used to run past it.
					for i, l := range lines {
						if w := lipgloss.Width(l); w != layer.Width() {
							t.Errorf("line %d is %d cells wide, the panel is %d: %q", i, w, layer.Width(), l)
						}
					}
				}
				if !found {
					t.Fatal("no whichkey layer rendered")
				}
				// The whole frame, with the menu over the panes: the compositor
				// takes a panel taller or wider than the screen.
				if _, ok := m.safeComposeFrame(); !ok {
					t.Errorf("drawing the frame with the menu up crashed")
				}
			})
		}
	}
}

// TestWhichKeyUnderADockThatTakesEveryRow: a top dock on a screen one or two
// rows tall leaves the menu no room. It used to lay out with no limit at all,
// a panel 30 rows tall on a screen of one. It takes the least panel now.
func TestWhichKeyUnderADockThatTakesEveryRow(t *testing.T) {
	for _, h := range []int{1, 2} {
		m := newNarrowOS(t, 80, h)
		m.Settings.DockbarPosition = "top"
		m.PrefixActive = true
		m.LastPrefixTime = time.Now().Add(-time.Hour)
		for _, layer := range m.renderOverlays() {
			if layer.GetID() == "whichkey" && layer.Height() > 7 {
				t.Errorf("80x%d under a top dock: the menu is %d rows tall", h, layer.Height())
			}
		}
	}
}

// TestWhichKeyKeepsALayoutWithNoColumns: a screen too small for any binding
// lays out to no columns, and that is kept like any other layout rather than
// laid out again on every frame the menu is up.
func TestWhichKeyKeepsALayoutWithNoColumns(t *testing.T) {
	m := newNarrowOS(t, 20, 4)
	m.PrefixActive = true
	m.LastPrefixTime = time.Now().Add(-time.Hour)
	title, groups := m.whichKeyMenu()
	cols, more := m.whichKeyLayout(title, groups, 1)
	if len(cols) != 0 || more == 0 {
		t.Fatalf("precondition: 20x4 laid out to %d columns and %d more, want none and a count", len(cols), more)
	}
	if !m.whichKeyCache.valid {
		t.Errorf("a layout with no columns was not kept")
	}
}
