package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// plugins/themes: a plugin of files only. It installs with no approval
// beyond the install, its themes load with the person's own, a file of the
// person's with the same id wins, and the themes go when the plugin is off.
//
// The negative controls are in NEGATIVE_CONTROLS.md, "Theme plugins".
func TestPluginThemes(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "")
	killDaemon(t, base)
	plugin, err := filepath.Abs(filepath.Join("..", "..", "plugins", "themes"))
	if err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "plugins", "check", plugin); err != nil || !strings.Contains(out, "themes   dusk, paper (2)") {
		t.Fatalf("check: %v\n%s", err, out)
	}
	// No daemon runs, so a plugin that runs something would wait for its
	// approval. This one runs nothing.
	out, err := tuiosCLI(t, base, "plugins", "install", plugin, "--yes")
	if err != nil || strings.Contains(out, "waits for your approval") {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if out, _ := tuiosCLI(t, base, "plugins", "list"); !strings.Contains(out, "tuios.themes  on") {
		t.Errorf("a plugin of files only does not run without an approval:\n%s", out)
	}
	themes := func() string {
		t.Helper()
		out, err := tuiosCLI(t, base, "--list-themes")
		if err != nil {
			t.Fatalf("--list-themes: %v\n%s", err, out)
		}
		return out
	}
	list := themes()
	for _, id := range []string{"dusk", "paper"} {
		if !strings.Contains("\n"+list, "\n"+id+"\n") {
			t.Errorf("--list-themes lacks the plugin theme %s", id)
		}
	}
	preview := func(id string) string {
		t.Helper()
		out, err := tuiosCLI(t, base, "--preview-theme", id)
		if err != nil {
			t.Fatalf("--preview-theme %s: %v\n%s", id, err, out)
		}
		// The colours only: the first line names the theme.
		_, colors, _ := strings.Cut(out, "\n")
		return colors
	}
	plugins := preview("dusk")

	// A theme of the person's own with the same id wins.
	mine := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "themes")
	if err := os.MkdirAll(mine, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mine, "dusk.json"), []byte(`{"id":"dusk","display_name":"My Dusk","base":"dracula"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if mine, dracula := preview("dusk"), preview("dracula"); mine == plugins || mine != dracula {
		t.Errorf("the plugin's dusk won over the person's own, which is dracula:\n%s", mine)
	}
	_ = os.Remove(filepath.Join(mine, "dusk.json"))

	// Off, the themes go.
	if out, err := tuiosCLI(t, base, "plugins", "disable", "tuios.themes"); err != nil {
		t.Fatalf("disable: %v\n%s", err, out)
	}
	if list := themes(); strings.Contains("\n"+list, "\npaper\n") {
		t.Errorf("a disabled plugin's theme still loads")
	}
}
