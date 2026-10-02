package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestTogglingTheSidebarPersists: a toggle the user asked for records itself
// on the held config and returns the write that puts it on disk. The next
// attach reads that file, so a toggle that only lives in memory makes the
// rail come back hidden forever.
func TestTogglingTheSidebarPersists(t *testing.T) {
	off := false
	cfg := &config.UserConfig{}
	cfg.Appearance.Sidebar.Enabled = &off
	m := &OS{UserConfig: cfg}

	for _, item := range GetCommandPaletteItems(&m.Settings) {
		if item.Name != "Toggle sidebar" {
			continue
		}
		next, cmd := item.Action(m)
		if cmd == nil {
			t.Error("the palette's sidebar toggle returned no config write")
		}
		if !next.Settings.SidebarEnabled || cfg.Appearance.Sidebar.Enabled == nil || !*cfg.Appearance.Sidebar.Enabled {
			t.Error("toggling the sidebar did not record enabled on the held config")
		}
		return
	}
	t.Error("the palette has no sidebar toggle")
}
