package config

import "testing"

// withDockWorkspaceTooltip restores the global an apply pass writes.
func withDockWorkspaceTooltip(t *testing.T) {
	t.Helper()
	prev := Global.DockWorkspaceTooltip
	t.Cleanup(func() { Global.DockWorkspaceTooltip = prev })
}

// TestDockWorkspaceTooltipExplicitFalseSurvivesApply is the other half: turning
// it off in the settings page has to survive a reload.
func TestDockWorkspaceTooltipExplicitFalseSurvivesApply(t *testing.T) {
	withDockWorkspaceTooltip(t)
	Global.DockWorkspaceTooltip = true

	cfg := loadTOML(t, `
[appearance]
dock_workspace_tooltip = false
`)
	ApplyAppearanceConfig(cfg, &Global)
	if Global.DockWorkspaceTooltip {
		t.Error("an explicit false was dropped")
	}
}
