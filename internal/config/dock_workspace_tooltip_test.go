package config

import "testing"

// withDockWorkspaceTooltip restores the global an apply pass writes.
func withDockWorkspaceTooltip(t *testing.T) {
	t.Helper()
	prev := DockWorkspaceTooltip
	t.Cleanup(func() { DockWorkspaceTooltip = prev })
}

// TestDockWorkspaceTooltipDefaultsOn is the migration half of the tri-state: a
// config file written before the key existed has no line for it, and must come
// up with the label on rather than reading the absence as off.
func TestDockWorkspaceTooltipDefaultsOn(t *testing.T) {
	withDockWorkspaceTooltip(t)
	DockWorkspaceTooltip = true

	cfg := loadTOML(t, `
[appearance]
dock_workspace_tabs = true
`)
	if cfg.Appearance.DockWorkspaceTooltip != nil {
		t.Fatalf("an absent key parsed as non-nil: %v", *cfg.Appearance.DockWorkspaceTooltip)
	}
	ApplyAppearanceConfig(cfg)
	if !DockWorkspaceTooltip {
		t.Error("an old config file turned the label off by saying nothing")
	}
}

// TestDockWorkspaceTooltipExplicitFalseSurvivesApply is the other half: turning
// it off in the settings page has to survive a reload.
func TestDockWorkspaceTooltipExplicitFalseSurvivesApply(t *testing.T) {
	withDockWorkspaceTooltip(t)
	DockWorkspaceTooltip = true

	cfg := loadTOML(t, `
[appearance]
dock_workspace_tooltip = false
`)
	ApplyAppearanceConfig(cfg)
	if DockWorkspaceTooltip {
		t.Error("an explicit false was dropped")
	}
}
