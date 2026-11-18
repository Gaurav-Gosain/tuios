package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestSettingsPageSetsNoFileKey holds the settings page to the line between
// the option registry and config.FileKeys: no row of the page names a
// file-only key, and the write every row goes through refuses one.
// config.TestFileKeysCannotBeSetAtRunTime checks set-option and look apply.
func TestSettingsPageSetsNoFileKey(t *testing.T) {
	m := &OS{Settings: config.Global, Width: 120, Height: 40, SidebarAgentsSeen: true}
	open := true
	m.settingsAgentsOpen = &open
	for _, cat := range m.settingsCategories() {
		for _, item := range cat.Items {
			if item.Path != "" && config.IsFileKey(item.Path) {
				t.Errorf("the settings page has a row for %s, a key set in the file only", item.Path)
			}
		}
	}
	m.UserConfig = config.DefaultConfig()
	for _, k := range config.FileKeys() {
		path := strings.ReplaceAll(k.Path, "NAME", "sample")
		if err := m.setConfigFromRegistry(path, "1"); err == nil {
			t.Errorf("the settings page set %s, a key set in the file only", path)
		}
	}
}
