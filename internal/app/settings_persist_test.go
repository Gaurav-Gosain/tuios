package app

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// runSaveCmd is runSave for a caller that expects a save to have been queued.
func runSaveCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("the settings change handed back no save command")
	}
	return cmd()
}

// TestStaleSaveGivesWayToNewer covers the one thing an off-thread write can get
// wrong that an inline one could not: two saves in flight, the older landing
// last, and the file ending up holding the config from before the newer change.
func TestStaleSaveGivesWayToNewer(t *testing.T) {
	path := useTempConfig(t)
	swapBool(t, &config.Global.SidebarFileIcons, true)
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})
	focusSetting(t, m, "Sidebar", "File icons")

	first := m.SettingsAdjust(1) // file_icons -> false
	second := m.SettingsAdjust(1)
	if first == nil || second == nil {
		t.Fatal("expected a save command from each change")
	}
	// Out of order on purpose: the newer render lands, then the older one is
	// asked to write and must decline.
	runSaveCmd(t, second)
	runSaveCmd(t, first)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	want := "file_icons = true"
	if !m.Settings.SidebarFileIcons {
		want = "file_icons = false"
	}
	if !strings.Contains(string(data), want) {
		t.Errorf("a stale save overwrote a newer one; wanted %q in:\n%s", want, data)
	}
}
