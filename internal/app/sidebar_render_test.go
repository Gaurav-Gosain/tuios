package app

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// railFixtureWidth is the rail width the rail's row tests were measured at,
// the shipped width before v0.8.0. The shipped width is now 24, where an
// agent row's second line and the files section's read-only mark are cut
// short; these fixtures test what a row says, so they keep the room to say it.
const railFixtureWidth = 28

// sidebarTestOS builds an OS with a few local windows and the sidebar enabled.
func sidebarTestOS(t *testing.T, w, h int, pos string) *OS {
	t.Helper()
	m := newNarrowOS(t, w, h)
	m.CurrentWorkspace = 1
	m.SessionName = ""
	m.Windows = []*terminal.Window{
		{ID: "aaaaaaaa1111", CustomName: "editor", Width: 40, Height: 20, Workspace: 1, AgentState: "working"},
		{ID: "bbbbbbbb2222", CustomName: "a-very-long-window-name-that-will-not-fit", Width: 40, Height: 20, Workspace: 1, AgentState: "needs_input"},
		{ID: "cccccccc3333", CustomName: "logs", Width: 40, Height: 20, Workspace: 1},
	}
	m.FocusedWindow = 0
	withSidebar(t, true, pos, railFixtureWidth)
	m.Settings = config.Global
	// NewOS ran before withSidebar redirected the state dir, so it read the
	// tree the whole binary shares, where an earlier test may have saved an
	// order. Drop it, so the rows come out in the order set below.
	m.SidebarOrder = nil
	return m
}

// wideRail widens the rail past sidebarCompactWidth, for a test about the
// terminals section's own rows whose fixture panes run agents: on a compact
// rail those panes are listed in the agents section alone.
func wideRail(m *OS) {
	m.SidebarWidthPref = sidebarCompactWidth + 4
}

// spreadTestOS is sidebarTestOS with its windows spread over workspaces 1, 2
// and 4, which is what gives a terminal row something to tag: a pane not on
// the current workspace names the one it is on.
func spreadTestOS(t *testing.T, w, h int, pos string) *OS {
	t.Helper()
	m := sidebarTestOS(t, w, h, pos)
	m.Windows[1].Workspace = 2
	m.Windows[2].Workspace = 4
	return m
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

// TestSidebarGlyphsAndCountsOff checks the sidebar still lays out to exact width
// with the optional row elements disabled.
func TestSidebarGlyphsAndCountsOff(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	pg, pc := m.Settings.SidebarShowGlyphs, m.Settings.SidebarShowCounts
	m.Settings.SidebarShowGlyphs, m.Settings.SidebarShowCounts = false, false
	t.Cleanup(func() { m.Settings.SidebarShowGlyphs, m.Settings.SidebarShowCounts = pg, pc })
	// The terminals section comes off the rail the only way it now can: it is
	// left out of the layout.
	withSections(t, "sessions:25,files:25,agents:34")
	m.Settings = config.Global

	lines, w := m.sidebarPanelLines()
	for i, ln := range lines {
		if lipgloss.Width(ln) != w {
			t.Errorf("row %d width %d, want %d", i, lipgloss.Width(ln), w)
		}
	}
}

// TestSidebarClickFocusesWindow checks a click on a window row focuses that
// window (the hit-test routes to the right target).
func TestSidebarClickFocusesWindow(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")

	// Render to populate the hit geometry.
	if _, w := m.sidebarPanelLines(); w == 0 {
		t.Fatalf("sidebar reserved no width")
	}

	// Find the window row for the third window (index 2).
	var target sidebarRowHit
	found := false
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowWindow && h.WindowIndex == 2 {
			target = h
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no window row recorded for window index 2; hits=%d", len(m.SidebarHits))
	}

	consumed := m.SidebarClick(target.X0+1, target.Y0, false)
	if !consumed {
		t.Fatalf("click in the sidebar band was not consumed")
	}
	if m.FocusedWindow != 2 {
		t.Errorf("focused window = %d, want 2", m.FocusedWindow)
	}
}
