package app

import (
	"os"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestConfigReadOnlySessionAppliesWithoutWriting pins the contract a served
// session gets: the change takes effect for that session, and the config file
// it was loaded from is left exactly as it was. tuios-web serves several
// clients from one process, each holding the snapshot it loaded when it
// connected, so a save would write one client's stale view over everyone's.
func TestConfigReadOnlySessionAppliesWithoutWriting(t *testing.T) {
	path := useTempConfig(t)
	// A file with a value the panel is about to change, so a write that did
	// happen is visible as a changed file rather than as a created one.
	if err := config.WriteConfigFile(config.DefaultConfig(), path); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read seeded config: %v", err)
	}

	// Both globals the panel is driven through, restored afterwards: a
	// read-only session still applies its change, so without this the second
	// toggle below leaks out of the test.
	swapBool(t, &config.SidebarFileIcons, true)
	swapBool(t, &config.SidebarMarquee, true)
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig(), ConfigReadOnly: true})

	focusSetting(t, m, "Sidebar", "File icons")
	m.SettingsAdjust(1)

	if config.SidebarFileIcons {
		t.Error("the change did not apply live; a read-only session still runs the setting")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-read config: %v", err)
	}
	if string(after) != string(before) {
		t.Error("a read-only session wrote the config file")
	}

	var told int
	for _, n := range m.Notifications {
		if strings.Contains(n.Message, "this session only") {
			told++
		}
	}
	if told != 1 {
		t.Errorf("raised the not-saved notice %d times, want exactly 1", told)
	}

	// Changing a second setting must not raise it again, or the panel becomes
	// unusable behind a stack of the same message.
	focusSetting(t, m, "Sidebar", "Marquee")
	m.SettingsAdjust(1)
	told = 0
	for _, n := range m.Notifications {
		if strings.Contains(n.Message, "this session only") {
			told++
		}
	}
	if told != 1 {
		t.Errorf("after a second change the notice appears %d times, want 1", told)
	}
}

// TestConfigReadOnlyShowsInSettingsTitle checks the panel says so, since a
// notification is gone by the time the second setting is changed.
func TestConfigReadOnlyShowsInSettingsTitle(t *testing.T) {
	useTempConfig(t)
	for _, readOnly := range []bool{false, true} {
		m := NewOS(OSOptions{UserConfig: config.DefaultConfig(), ConfigReadOnly: readOnly, Width: 120, Height: 40})
		m.OpenSettings()
		body, _, _ := m.renderSettings()
		if got := strings.Contains(body, "this session only"); got != readOnly {
			t.Errorf("ConfigReadOnly=%v: title marker present=%v", readOnly, got)
		}
	}
}

// TestWritableSessionStillPersists is the other half: the default is unchanged,
// so a local tuios keeps saving its settings.
func TestWritableSessionStillPersists(t *testing.T) {
	useTempConfig(t)
	swapBool(t, &config.SidebarFileIcons, true)
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})

	focusSetting(t, m, "Sidebar", "File icons")
	runSave(t, m.SettingsAdjust(1))

	reloaded, err := config.LoadUserConfig()
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if got := reloaded.Appearance.Sidebar.FileIcons; got == nil || *got {
		t.Error("a writable session did not persist the change")
	}
}
