package app

import (
	"regexp"
	"strings"
	"testing"

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
