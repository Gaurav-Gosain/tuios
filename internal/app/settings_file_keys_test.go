package app

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

type fileOnlyKey struct{ path, typ string }

func fileOnlyKeys(t *testing.T) []fileOnlyKey {
	t.Helper()
	data, err := os.ReadFile("../../docs/config-options.md")
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(data), "\n## Keys set in the file only\n")
	if !ok {
		t.Fatal("docs/config-options.md has no section \"Keys set in the file only\"")
	}
	row := regexp.MustCompile("(?m)^\\| <a id=\"([^\"]+)\"></a>`[^`]+` \\| ([a-z]+) \\|")
	var keys []fileOnlyKey
	for _, m := range row.FindAllStringSubmatch(section, -1) {
		keys = append(keys, fileOnlyKey{m[1], m[2]})
	}
	if len(keys) < 50 {
		t.Fatalf("found %d file-only keys in docs/config-options.md, want the whole section", len(keys))
	}
	return keys
}

func (k fileOnlyKey) covers(path string) bool {
	want, got := strings.Split(k.path, "."), strings.Split(path, ".")
	table := k.typ == "table" || k.typ == "tables" || k.typ == "map"
	if len(got) < len(want) || (!table && len(got) != len(want)) {
		return false
	}
	for i, w := range want {
		if w != "NAME" && w != got[i] {
			return false
		}
	}
	return true
}

// TestSettingsPageSetsNoFileKey holds the settings page to the line between
// the option registry and the keys set in the file only: no row of the page
// names such a key, and the write every row goes through refuses one.
// config.TestFileKeysCannotBeSetAtRunTime checks set-option and look apply.
func TestSettingsPageSetsNoFileKey(t *testing.T) {
	keys := fileOnlyKeys(t)
	m := &OS{Settings: config.Global, Width: 120, Height: 40, SidebarAgentsSeen: true}
	open := true
	m.settingsAgentsOpen = &open
	for _, cat := range m.settingsCategories() {
		for _, item := range cat.Items {
			for _, k := range keys {
				if item.Path != "" && k.covers(item.Path) {
					t.Errorf("the settings page has a row for %s, a key set in the file only", item.Path)
				}
			}
		}
	}
	m.UserConfig = config.DefaultConfig()
	for _, k := range keys {
		path := strings.ReplaceAll(k.path, "NAME", "sample")
		if err := m.setConfigFromRegistry(path, "1"); err == nil {
			t.Errorf("the settings page set %s, a key set in the file only", path)
		}
	}
}
