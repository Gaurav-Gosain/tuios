package app

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

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

	first := m.SettingsAdjust(1)
	second := m.SettingsAdjust(1)
	if first == nil || second == nil {
		t.Fatal("expected a save command from each change")
	}
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

func runSave(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	return cmd()
}

// TestSettingsWriteHappensInTheCommand is the shape of the fix, not just its
// effect: the config file must be untouched when the setter returns and written
// only once the command runs. Asserting the end state alone would pass just as
// well with the write back inline on the Update goroutine, which is the thing
// this is here to stop.
func TestSettingsWriteHappensInTheCommand(t *testing.T) {
	path := useTempConfig(t)
	if err := config.WriteConfigFile(config.DefaultConfig(), path); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read seeded config: %v", err)
	}

	swapBool(t, &config.Global.SidebarFileIcons, true)
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})
	focusSetting(t, m, "Sidebar", "File icons")
	cmd := m.SettingsAdjust(1)

	if m.Settings.SidebarFileIcons {
		t.Error("the change did not apply live")
	}
	mid, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-read config: %v", err)
	}
	if string(mid) != string(before) {
		t.Error("the config file was written before the command ran; the write is still on the Update goroutine")
	}

	if msg := runSaveCmd(t, cmd); msg != nil {
		t.Errorf("the save command reported %#v", msg)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written config: %v", err)
	}
	if string(after) == string(before) {
		t.Error("running the save command wrote nothing")
	}
	if !strings.Contains(string(after), "file_icons = false") {
		t.Errorf("the written config does not carry the change:\n%s", after)
	}
}
