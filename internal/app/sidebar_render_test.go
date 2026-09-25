package app

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// wideRail widens the rail past sidebarCompactWidth, for a test about the
// terminals section's own rows whose fixture panes run agents: on a compact
// rail those panes are listed in the agents section alone.
func wideRail(m *OS) {
	m.SidebarWidthPref = sidebarCompactWidth + 4
}

// TestSidebarFitsNarrowScreens renders the sidebar at a range of sizes and
// asserts every row is exactly the reserved width (never overflowing, never a
// negative or control-padded width), the column is the usable height tall, and
// the recorded hits sit inside the reserved band.
func TestSidebarFitsNarrowScreens(t *testing.T) {
	sizes := []struct {
		name  string
		w, h  int
		wantW int // 0 means auto-hidden
	}{
		{"desktop", 120, 40, railFixtureWidth},
		{"narrow-rail", 80, 24, config.SidebarNarrowWidth},
		{"glyph-rail", 51, 37, config.SidebarGlyphWidth},
		{"auto-hidden", 30, 24, 0},
		{"glyph-boundary", 40, 20, config.SidebarGlyphWidth},
	}
	for _, pos := range []string{"left", "right"} {
		for _, sz := range sizes {
			t.Run(fmt.Sprintf("%s/%s", pos, sz.name), func(t *testing.T) {
				m := sidebarTestOS(t, sz.w, sz.h, pos)

				lines, w := m.sidebarPanelLines()
				if sz.wantW == 0 {
					if lines != nil {
						t.Fatalf("expected auto-hidden sidebar, got %d rows", len(lines))
					}
					return
				}
				if w != sz.wantW {
					t.Errorf("width = %d, want %d", w, sz.wantW)
				}
				if w <= 0 {
					t.Fatalf("non-positive sidebar width %d", w)
				}
				if got := len(lines); got != m.GetUsableHeight() {
					t.Errorf("row count = %d, want usable height %d", got, m.GetUsableHeight())
				}
				for i, ln := range lines {
					if j := strings.IndexAny(ln, "\t\r\v\f"); j >= 0 {
						t.Errorf("row %d pads with a control character %q", i, ln[j])
					}
					if lw := lipgloss.Width(ln); lw != w {
						t.Errorf("row %d is %d cells wide, want exactly %d: %q", i, lw, w, ln)
					}
				}

				topMargin := m.GetTopMargin()
				sidebarX := 0
				if pos == "right" {
					sidebarX = m.GetRenderWidth() - w
				}
				for _, hit := range m.SidebarHits {
					// A row hit claims the whole band. A control that shares its
					// line with a label or a sibling (the headers' add controls,
					// the agents header's filter and sort, the footer's toggle)
					// claims only its own columns and has to stay inside them.
					switch hit.Kind {
					case sidebarRowNewSession, sidebarRowNewWindow, sidebarRowCollapse,
						sidebarRowAgentSort, sidebarRowAgentFilter, sidebarRowAgentMail, sidebarRowFiles:
						if hit.X0 < sidebarX || hit.X1 > sidebarX+w || hit.X0 >= hit.X1 {
							t.Errorf("zone hit X range [%d,%d) outside the sidebar band [%d,%d)",
								hit.X0, hit.X1, sidebarX, sidebarX+w)
						}
					default:
						if hit.X0 != sidebarX || hit.X1 != sidebarX+w {
							t.Errorf("hit X range [%d,%d) not the sidebar band [%d,%d)", hit.X0, hit.X1, sidebarX, sidebarX+w)
						}
					}
					if hit.Y0 < topMargin || hit.Y0 >= topMargin+m.GetUsableHeight() {
						t.Errorf("hit Y %d outside the sidebar band", hit.Y0)
					}
				}
			})
		}
	}
}
