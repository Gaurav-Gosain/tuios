package app

import (
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// ansiEscape strips SGR sequences, leaving the cells a terminal would show. A
// frame read this way is also the monochrome frame: it is what is left when the
// terminal has no palette to give.
var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;]*m")

// plainFrame renders the settings panel and returns its lines with the colour
// dropped, plus the geometry the renderer recorded as it drew them.
func plainFrame(t *testing.T, m *OS) ([]string, overlay.Geometry) {
	t.Helper()
	content, geo, _ := m.renderSettings()
	lines := strings.Split(content, "\n")
	for i, ln := range lines {
		lines[i] = ansiEscape.ReplaceAllString(ln, "")
	}
	return lines, geo
}

// tabRowOf returns the one row the tab strip drew, taken from the geometry
// rather than by searching the frame.
func tabRowOf(t *testing.T, lines []string, geo overlay.Geometry) string {
	t.Helper()
	y := -1
	for _, r := range geo.Tabs {
		if !r.Empty() {
			y = r.Y0
			break
		}
	}
	if y < 0 {
		t.Fatal("no tab was drawn")
	}
	return lines[y]
}

// settingsTabNames is the section list the strip has to carry.
func settingsTabNames(m *OS) []string {
	cats := m.settingsCategories()
	names := make([]string, len(cats))
	for i, c := range cats {
		names[i] = c.Name
	}
	return names
}

// TestSettingsPanelWidthTracksTheTerminal pins the panel's width in the rendered
// frame at a narrow, a normal and a very wide terminal. The panel used to ask
// for 62 columns whatever the screen was, so a 190-column terminal got a third
// of itself; it now grows with the terminal and stops at settingsMaxInnerWidth,
// past which a label on the left and its control on the right stop reading as
// one row.
func TestSettingsPanelWidthTracksTheTerminal(t *testing.T) {
	cases := []struct {
		name string
		term int
		want int // rendered block width, inner + 2 cells of pad each side
	}{
		{"narrow", 51, 51},     // screen-bound: the whole screen less nothing to spare
		{"normal", 100, 84},    // at the maximum
		{"very wide", 240, 84}, // still at the maximum
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newNarrowOS(t, tc.term, 40)
			lines, geo := plainFrame(t, m)
			if geo.Width != tc.want {
				t.Errorf("panel width = %d, want %d", geo.Width, tc.want)
			}
			for i, ln := range lines {
				if w := lipgloss.Width(ln); w != geo.Width {
					t.Fatalf("line %d is %d cells, panel is %d: %q", i, w, geo.Width, ln)
				}
			}
			if geo.InnerWidth > settingsMaxInnerWidth {
				t.Errorf("inner width = %d, past the maximum %d", geo.InnerWidth, settingsMaxInnerWidth)
			}
		})
	}
}

// TestSettingsTabRowIsOneLineAtEveryWidth sweeps every width the panel can be
// drawn at, with every section active in turn, and holds the strip to a single
// row. A second row of tabs is what made the panel look broken, and it would
// shift every body rect the row-indexed addressing below it depends on.
func TestSettingsTabRowIsOneLineAtEveryWidth(t *testing.T) {
	for w := 16; w <= 200; w++ {
		m := newNarrowOS(t, w, 40)
		names := settingsTabNames(m)
		for active := range names {
			m.SettingsCategory, m.SettingsSelected = active, 0
			lines, geo := plainFrame(t, m)

			rows := map[int]bool{}
			drawn := 0
			for _, r := range geo.Tabs {
				if r.Empty() {
					continue
				}
				rows[r.Y0] = true
				drawn++
			}
			if len(rows) != 1 {
				t.Fatalf("w=%d active=%d: tabs occupy %d rows, want 1", w, active, len(rows))
			}
			if drawn == 0 {
				t.Fatalf("w=%d active=%d: no tab drawn", w, active)
			}
			if geo.Tabs[active].Empty() {
				t.Fatalf("w=%d: the active tab %q was not drawn", w, names[active])
			}

			// The affordance appears only when there is something that way.
			row := tabRowOf(t, lines, geo)
			first, last := -1, -1
			for i, r := range geo.Tabs {
				if r.Empty() {
					continue
				}
				if first < 0 {
					first = i
				}
				last = i
			}
			leftGlyph, rightGlyph := "‹", "›"
			wantLeft, wantRight := first > 0, last < len(names)-1
			if got := !geo.TabPrev.Empty(); got != wantLeft {
				t.Fatalf("w=%d active=%d: left arrow=%v, want %v (first drawn tab %d)", w, active, got, wantLeft, first)
			}
			if got := !geo.TabNext.Empty(); got != wantRight {
				t.Fatalf("w=%d active=%d: right arrow=%v, want %v (last drawn tab %d)", w, active, got, wantRight, last)
			}
			if !wantLeft && !wantRight && (strings.Contains(row, leftGlyph) || strings.Contains(row, rightGlyph)) {
				t.Fatalf("w=%d: every tab fits yet the row carries an arrow: %q", w, row)
			}
			if !geo.TabPrev.Empty() && string([]rune(row)[geo.TabPrev.X0]) != leftGlyph {
				t.Fatalf("w=%d: left arrow rect is not over the glyph: %q", w, row)
			}
		}
	}
}

// TestSettingsActiveTabScrollsIntoView walks every section by keyboard on a
// terminal too narrow to show them all, and requires the section just switched
// to be on screen each time. A tab that is active but scrolled away leaves the
// strip saying nothing about where the body under it came from.
func TestSettingsActiveTabScrollsIntoView(t *testing.T) {
	check := func(t *testing.T, m *OS, step string) {
		t.Helper()
		lines, geo := plainFrame(t, m)
		names := settingsTabNames(m)
		r := geo.Tabs[m.SettingsCategory]
		if r.Empty() {
			t.Fatalf("%s: active tab %q is not drawn:\n%s", step, names[m.SettingsCategory], tabRowOf(t, lines, geo))
		}
		row := []rune(tabRowOf(t, lines, geo))
		span := strings.TrimSpace(string(row[r.X0:r.X1]))
		if span != names[m.SettingsCategory] && !strings.HasPrefix(names[m.SettingsCategory], strings.TrimSuffix(span, "…")) {
			t.Fatalf("%s: rect over %q, want %q", step, span, names[m.SettingsCategory])
		}
	}

	m := newNarrowOS(t, 44, 40)
	n := len(settingsTabNames(m))

	// Forward off the right-hand end and back off the left.
	for i := 1; i < n; i++ {
		m.SettingsNextCategory()
		check(t, m, "tab forward to "+settingsTabNames(m)[m.SettingsCategory])
	}
	for i := 1; i < n; i++ {
		m.SettingsPrevCategory()
		check(t, m, "shift-tab back to "+settingsTabNames(m)[m.SettingsCategory])
	}

	// A jump straight to a section that was off screen, which is what wrapping
	// round from the first to the last does.
	m.SettingsCategory = 0
	_, _ = plainFrame(t, m)
	m.SettingsPrevCategory()
	check(t, m, "wrap to the last section")
}

// TestSettingsTabRectsMatchDrawnCells checks every recorded tab rect against the
// cells the renderer actually drew, both edge columns included, and confirms a
// click on either edge lands on that tab. The strip scrolls, so its targets move
// between frames and a recomputed rect would drift off them.
func TestSettingsTabRectsMatchDrawnCells(t *testing.T) {
	for _, w := range []int{44, 100, 190} {
		for _, active := range []int{0, 3, 7} {
			m := newNarrowOS(t, w, 40)
			m.ShowSettings = true
			m.SettingsCategory = active
			names := settingsTabNames(m)
			lines, geo := plainFrame(t, m)
			row := []rune(tabRowOf(t, lines, geo))

			prevX1 := -1
			for i, r := range geo.Tabs {
				if r.Empty() {
					continue
				}
				if r.X0 < 0 || r.X1 > len(row) {
					t.Fatalf("w=%d: tab %q rect %v is off the row", w, names[i], r)
				}
				span := string(row[r.X0:r.X1])
				if got := strings.TrimSpace(span); got != names[i] {
					t.Errorf("w=%d active=%d: tab %d rect covers %q, want %q", w, active, i, got, names[i])
				}
				// Both edge columns belong to this tab and nothing else: the span
				// holds the whole label, so neither edge has eaten a neighbour's
				// cell nor left one of its own outside.
				if !strings.Contains(span, names[i]) {
					t.Errorf("w=%d: tab %d span %q lost an edge column of %q", w, i, span, names[i])
				}
				if prevX1 >= 0 && r.X0 < prevX1 {
					t.Errorf("w=%d: tab %d starts at %d, inside the previous tab ending at %d", w, i, r.X0, prevX1)
				}
				prevX1 = r.X1
			}

			// A click on the first and last column of each drawn tab selects it.
			m.renderSettingsHit()
			h := m.settingsHit()
			for i, r := range h.Geo.Tabs {
				if r.Empty() {
					continue
				}
				for _, col := range []int{r.X0, r.X1 - 1} {
					m.SettingsCategory = active
					m.renderSettingsHit()
					h = m.settingsHit()
					handled, _ := m.OverlayMouseClick(h.OriginX+col, h.OriginY+r.Y0, false)
					if !handled || m.SettingsCategory != i {
						t.Errorf("w=%d: click at column %d of tab %q selected %d, want %d",
							w, col, names[i], m.SettingsCategory, i)
					}
				}
			}
		}
	}
}

// TestSettingsTabOverflowArrowsStep confirms the strip's arrows reach the
// sections they point at.
func TestSettingsTabOverflowArrowsStep(t *testing.T) {
	m := newNarrowOS(t, 44, 40)
	m.ShowSettings = true
	m.SettingsCategory = 5
	m.renderSettingsHit()
	h := m.settingsHit()
	if h.Geo.TabPrev.Empty() || h.Geo.TabNext.Empty() {
		t.Fatalf("expected both arrows with section 5 active on a 44-column screen")
	}
	m.OverlayMouseClick(h.OriginX+h.Geo.TabNext.X0, h.OriginY+h.Geo.TabNext.Y0, false)
	if m.SettingsCategory != 6 {
		t.Errorf("right arrow moved to section %d, want 6", m.SettingsCategory)
	}
	m.renderSettingsHit()
	h = m.settingsHit()
	m.OverlayMouseClick(h.OriginX+h.Geo.TabPrev.X0, h.OriginY+h.Geo.TabPrev.Y0, false)
	if m.SettingsCategory != 5 {
		t.Errorf("left arrow moved to section %d, want 5", m.SettingsCategory)
	}
}

// TestSettingsTabRowASCIIAndMonochrome: the strip's overflow marks are the only
// thing saying there are sections off screen, so they have to survive a terminal
// with no glyphs and one with no colour. In ASCII they are plain angle brackets;
// in monochrome, where the active pill's accent fill is gone, they are still
// there because they are glyphs rather than colour.
func TestSettingsTabRowASCIIAndMonochrome(t *testing.T) {
	t.Run("ascii", func(t *testing.T) {
		prev := config.Global.UseASCIIOnly
		config.Global.UseASCIIOnly = true
		overlay.SetASCII(true)
		t.Cleanup(func() {
			config.Global.UseASCIIOnly = prev
			overlay.SetASCII(prev)
		})

		m := newNarrowOS(t, 44, 40)
		m.SettingsCategory = 5
		lines, geo := plainFrame(t, m)
		row := tabRowOf(t, lines, geo)
		for _, glyph := range []string{"‹", "›", "…"} {
			if strings.Contains(row, glyph) {
				t.Errorf("the ASCII tab row still draws %q: %q", glyph, row)
			}
		}
		if !strings.Contains(row, "<") || !strings.Contains(row, ">") {
			t.Errorf("the ASCII tab row lost its overflow marks: %q", row)
		}
		if len(strings.Split(strings.Join(lines, "\n"), "\n")) < 1 {
			t.Fatal("no frame")
		}
	})

	t.Run("monochrome", func(t *testing.T) {
		m := newNarrowOS(t, 44, 40)
		m.SettingsCategory = 5
		lines, geo := plainFrame(t, m)
		row := tabRowOf(t, lines, geo)
		names := settingsTabNames(m)
		if !strings.Contains(row, "‹") || !strings.Contains(row, "›") {
			t.Errorf("monochrome loses the overflow marks: %q", row)
		}
		if !strings.Contains(row, names[5]) {
			t.Errorf("monochrome loses the active section name %q: %q", names[5], row)
		}
		// The active section keeps a mark that is not colour: its pill is padded.
		if !strings.Contains(row, " "+names[5]+" ") {
			t.Errorf("monochrome leaves nothing marking the active section: %q", row)
		}
	})
}
