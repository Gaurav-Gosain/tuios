package app

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

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
	withSidebar(t, true, pos, config.SidebarDefaultWidth)
	// NewOS ran before withSidebar redirected the state dir, so drop anything
	// it may have loaded from the developer's real state file.
	m.SidebarOrder, m.SidebarCollapsed = nil, nil
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
		{"desktop", 120, 40, config.SidebarDefaultWidth},
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
					if hit.X0 != sidebarX || hit.X1 != sidebarX+w {
						t.Errorf("hit X range [%d,%d) not the sidebar band [%d,%d)", hit.X0, hit.X1, sidebarX, sidebarX+w)
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
	pg, pc, pw := config.SidebarShowGlyphs, config.SidebarShowCounts, config.SidebarShowWindows
	config.SidebarShowGlyphs, config.SidebarShowCounts, config.SidebarShowWindows = false, false, false
	t.Cleanup(func() {
		config.SidebarShowGlyphs, config.SidebarShowCounts, config.SidebarShowWindows = pg, pc, pw
	})

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

// TestSidebarClickTogglesCurrentSession checks that clicking the current session
// row toggles its expand/collapse state rather than switching.
func TestSidebarClickTogglesCurrentSession(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	m.sidebarPanelLines()

	var sessionRow sidebarRowHit
	found := false
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowSession {
			sessionRow = h
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no session row recorded")
	}

	// The current (local) session starts expanded; a click collapses it.
	m.SidebarClick(sessionRow.X0+1, sessionRow.Y0, false)
	if !m.SidebarCollapsed[sessionRow.SessionID] {
		t.Errorf("session %q not collapsed after click", sessionRow.SessionID)
	}
	// A second click expands it again.
	m.SidebarClick(sessionRow.X0+1, sessionRow.Y0, false)
	if m.SidebarCollapsed[sessionRow.SessionID] {
		t.Errorf("session %q not re-expanded after second click", sessionRow.SessionID)
	}
}

// TestSidebarWheelScrollsList checks the wheel over the band moves the scroll and
// is consumed, and that a wheel outside the band is ignored.
func TestSidebarWheelScrollsList(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	m.sidebarPanelLines()

	if !m.SidebarWheel(1, m.GetTopMargin(), false) {
		t.Fatalf("wheel over the band was not consumed")
	}
	if m.SidebarScroll <= 0 {
		t.Errorf("scroll did not advance: %d", m.SidebarScroll)
	}
	// Outside the band (to the right of a left sidebar) is not the sidebar's.
	if m.SidebarWheel(m.GetRenderWidth()-1, m.GetTopMargin(), false) {
		t.Errorf("wheel outside the band was wrongly consumed")
	}
}
