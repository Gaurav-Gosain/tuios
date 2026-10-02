package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func TestLayoutModeFollowsTheWorkspace(t *testing.T) {
	m := &OS{
		Settings:            config.Global,
		Windows:             []*terminal.Window{},
		UserConfig:          &config.UserConfig{Startup: config.StartupConfig{Layout: config.LayoutModeMasterStack}},
		WorkspaceLayoutMode: make(map[int]string),
		NumWorkspaces:       3,
		CurrentWorkspace:    1,
		AutoTiling:          true,
	}
	m.UseStackedLayout = true

	m.recordWorkspaceLayoutMode()
	if got := m.WorkspaceLayoutMode[1]; got != config.LayoutModeStacked {
		t.Fatalf("workspace 1 recorded %q, want stacked", got)
	}

	// A workspace that never chose follows the configured default rather than
	// inheriting the mode of the workspace that was last on screen.
	m.WorkspaceLayoutMode[2] = config.LayoutModeScrolling
	m.RestoreWorkspaceLayout(2)
	if !m.UseScrollingLayout {
		t.Fatalf("workspace 2 chose scrolling, mode is scrolling=%v bsp=%v stacked=%v",
			m.UseScrollingLayout, m.UseBSPLayout, m.UseStackedLayout)
	}

	m.RestoreWorkspaceLayout(3)
	if m.UseStackedLayout {
		t.Fatalf("workspace 3 never chose a layout, so it follows the config default, got stacked")
	}

	m.RestoreWorkspaceLayout(1)
	if !m.UseStackedLayout {
		t.Fatalf("workspace 1 chose stacked, got scrolling=%v bsp=%v", m.UseScrollingLayout, m.UseBSPLayout)
	}
}
