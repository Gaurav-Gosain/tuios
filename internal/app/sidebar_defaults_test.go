package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestDefaultSidebarOnNarrowTerminals runs the shipped rail (on, on the right,
// 24 columns wide) and the shipped dock (on top) against screens from wide
// down to tiny. The rail steps down to its narrow and glyph variants and then
// hides, and at no width does it leave the panes fewer than the pane floor.
func TestDefaultSidebarOnNarrowTerminals(t *testing.T) {
	cases := []struct {
		width     int
		wantRail  int
		wantPanes int
	}{
		{200, config.SidebarDefaultWidth, 200 - config.SidebarDefaultWidth},
		{120, config.SidebarDefaultWidth, 120 - config.SidebarDefaultWidth},
		{90, config.SidebarDefaultWidth, 90 - config.SidebarDefaultWidth},
		{89, config.SidebarNarrowWidth, 89 - config.SidebarNarrowWidth},
		{80, config.SidebarNarrowWidth, 80 - config.SidebarNarrowWidth},
		{79, config.SidebarNarrowWidth, 79 - config.SidebarNarrowWidth},
		{60, config.SidebarNarrowWidth, 60 - config.SidebarNarrowWidth},
		{59, config.SidebarGlyphWidth, 59 - config.SidebarGlyphWidth},
		{45, config.SidebarGlyphWidth, 45 - config.SidebarGlyphWidth},
		{40, config.SidebarGlyphWidth, 40 - config.SidebarGlyphWidth},
		{39, 0, 39},
		{20, 0, 20},
		{1, 0, 1},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%d columns", c.width), func(t *testing.T) {
			m := &OS{Settings: config.DefaultSettings(), Width: c.width, Height: 30}
			if got := m.GetSidebarWidth(); got != c.wantRail {
				t.Errorf("rail = %d, want %d", got, c.wantRail)
			}
			if m.GetLeftMargin() != 0 {
				t.Errorf("the rail is on the right, yet the left margin is %d", m.GetLeftMargin())
			}
			if got := m.GetRightMargin(); got != c.wantRail {
				t.Errorf("right margin = %d, want %d", got, c.wantRail)
			}
			if got := m.GetContentWidth(); got != c.wantPanes {
				t.Errorf("pane width = %d, want %d", got, c.wantPanes)
			}
			if c.wantRail > 0 && m.GetContentWidth() < config.SidebarMinPaneFloor {
				t.Errorf("pane width %d is below the floor %d", m.GetContentWidth(), config.SidebarMinPaneFloor)
			}
			if m.GetTopMargin() != config.DockHeight || m.GetBottomMargin() != 0 {
				t.Errorf("dock margins top=%d bottom=%d, want the dock on top", m.GetTopMargin(), m.GetBottomMargin())
			}
		})
	}
}

// TestDefaultSidebarTilesOnNarrowTerminals tiles panes under the shipped rail
// through the daemon path at the widths a laptop split or a phone gives, and
// checks every pane keeps a usable width inside the content box.
func TestDefaultSidebarTilesOnNarrowTerminals(t *testing.T) {
	prev, prevDir := config.Global, sidebarStateDir
	t.Cleanup(func() { config.Global, sidebarStateDir = prev, prevDir })
	config.Global = config.DefaultSettings()
	dir := t.TempDir()
	sidebarStateDir = func() string { return dir }

	for _, width := range []int{120, 80, 79, 60, 45, 40, 39} {
		t.Run(fmt.Sprintf("%d columns", width), func(t *testing.T) {
			m := tileDaemonWindowsMode(t, width, 30, 2, "bsp")
			if len(m.Windows) != 2 {
				t.Fatalf("tiled %d panes, want 2", len(m.Windows))
			}
			if !m.Settings.SidebarEnabled || m.Settings.SidebarPosition != "right" {
				t.Fatalf("the fixture is not on the shipped rail: enabled=%v position=%q",
					m.Settings.SidebarEnabled, m.Settings.SidebarPosition)
			}
			left := m.GetLeftMargin()
			right := left + m.GetContentWidth()
			for _, w := range m.Windows {
				if w.X < left || w.X+w.Width > right {
					t.Errorf("pane %s spans [%d,%d), outside the content box [%d,%d)", w.ID, w.X, w.X+w.Width, left, right)
				}
				if w.Width < 10 {
					t.Errorf("pane %s is %d columns wide", w.ID, w.Width)
				}
			}
		})
	}
}
