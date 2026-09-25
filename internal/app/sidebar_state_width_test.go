package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestToggleSidebarSurvivesASave checks a toggle lands in the config the next
// save writes. The first-run config names [appearance.sidebar] enabled, and
// the toggle used to write the legacy flat key, which the load migration
// drops whenever the table names the key, so turning the shipped rail off
// came back on at the next reload.
func TestToggleSidebarSurvivesASave(t *testing.T) {
	for _, start := range []bool{true, false} {
		t.Run(fmt.Sprintf("from %v", start), func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Appearance.Sidebar.Enabled = new(start)
			s := config.AppearanceFrom(cfg, config.Overrides{})
			m := &OS{Settings: s, UserConfig: cfg, Width: 120, Height: 40}
			m.ToggleSidebar()

			// What a reload of the saved file applies.
			again := config.AppearanceFrom(cfg, config.Overrides{})
			if again.SidebarEnabled == start {
				t.Errorf("toggled from %v, but the saved config still says %v", start, again.SidebarEnabled)
			}
		})
	}
}

// TestSidebarStateWidthOnlyWhenDragged pins what the state file does with the
// rail width. An older release wrote the config width into it whether or not
// anyone had dragged, so a file without drag_width holding the old default is
// read as no preference, and the current default applies.
func TestSidebarStateWidthOnlyWhenDragged(t *testing.T) {
	cases := []struct {
		name string
		file string
		want int
	}{
		{"legacy file at the old default", `{"width":28}`, 0},
		{"legacy file at a dragged width", `{"width":40}`, 40},
		{"current file at a dragged 28", `{"width":28,"drag_width":true}`, 28},
		{"current file never dragged", `{"drag_width":true}`, 0},
		{"no width at all", `{}`, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			prevDir := sidebarStateDir
			sidebarStateDir = func() string { return dir }
			t.Cleanup(func() { sidebarStateDir = prevDir })
			if err := os.WriteFile(filepath.Join(dir, sidebarStateFileName), []byte(c.file), 0o600); err != nil {
				t.Fatal(err)
			}
			m := &OS{Settings: config.DefaultSettings(), Width: 120, Height: 40}
			m.loadSidebarState()
			if m.SidebarWidthPref != c.want {
				t.Errorf("width preference = %d, want %d", m.SidebarWidthPref, c.want)
			}
			if c.want == 0 && m.GetSidebarWidth() != config.SidebarDefaultWidth {
				t.Errorf("rail = %d, want the default %d", m.GetSidebarWidth(), config.SidebarDefaultWidth)
			}
		})
	}

	for _, c := range []struct {
		name      string
		pref      int
		wantWidth int
	}{
		{"a save with no drag writes no width", 0, 0},
		{"a save after a drag writes the dragged width", 40, 40},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			prevDir := sidebarStateDir
			sidebarStateDir = func() string { return dir }
			t.Cleanup(func() { sidebarStateDir = prevDir })
			m := &OS{Settings: config.DefaultSettings(), Width: 120, Height: 40, SidebarWidthPref: c.pref}
			m.saveSidebarState()
			data, err := os.ReadFile(filepath.Join(dir, sidebarStateFileName))
			if err != nil {
				t.Fatal(err)
			}
			var st sidebarStateFile
			if err := json.Unmarshal(data, &st); err != nil {
				t.Fatal(err)
			}
			if st.Width != c.wantWidth || !st.DragWidth {
				t.Errorf("saved width=%d drag_width=%v, want %d and true", st.Width, st.DragWidth, c.wantWidth)
			}
		})
	}
}
