package theme

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tint "github.com/lrstanley/bubbletint/v2"
)

// writeThemeDir writes theme files into a fresh directory, reads it as the
// themes directory, and unregisters what it read when the test ends.
func writeThemeDir(t *testing.T, files map[string]string) (loaded, problems []string) {
	t.Helper()
	EnsureRegistry()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	loaded, problems, err := readCustomThemes(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, id := range loaded {
			tint.UnregisterIDs(id)
			registerChrome(id, nil)
		}
	})
	return loaded, problems
}

// TestAThemeInheritsItsBase: what a file leaves out comes from its base, the
// base may be another file listed in any order, and the chrome is resolved on
// the merged colours, so a slot name follows a colour the child changes.
//
// A built-in base passes on its chrome too, and a chrome field that is not a
// string costs that field, not the file.
//
// Negative controls: without the chrome merge, ember_red has no accent; without
// the colour merge, ember's background is the xterm black; without the
// built-in's chrome, ember_ct has no accent.
func TestAThemeInheritsItsBase(t *testing.T) {
	loaded, problems := writeThemeDir(t, map[string]string{
		"a_child.json":  `{"id": "ember_red", "base": "ember", "bright_red": "#123456"}`,
		"ember.json":    `{"id": "ember", "base": "dracula", "chrome": {"accent": "bright_red"}}`,
		"ember_ct.json": `{"id": "ember_ct", "base": "charmtone", "chrome": {"info": 5}}`,
	})
	if len(problems) > 0 || len(loaded) != 3 {
		t.Fatalf("loaded %v, problems %v", loaded, problems)
	}
	dracula, _ := tint.GetTint("dracula")
	ember, _ := tint.GetTint("ember")
	if hexOf(t, ember.Bg) != hexOf(t, dracula.Bg) || ember.Dark != dracula.Dark {
		t.Errorf("ember's background is %s and dark %v, want dracula's %s and %v",
			hexOf(t, ember.Bg), ember.Dark, hexOf(t, dracula.Bg), dracula.Dark)
	}
	for _, tc := range []struct{ id, want string }{
		{"ember", hexOf(t, dracula.BrightRed)},
		{"ember_red", "#123456"},
		{"ember_ct", "#6b50ff"},
	} {
		chromeRegistry.RLock()
		c := chromeRegistry.byID[tc.id]
		chromeRegistry.RUnlock()
		if c == nil || hexOf(t, c.Accent) != tc.want {
			t.Errorf("%s's accent is %v, want %s", tc.id, c, tc.want)
		}
	}
}

// TestABaseLoopFailsItsFiles: two files that name each other as base have no
// colours to start from. Both fail with a message that says why, the loader
// stops rather than spinning, and a third file in the directory still loads.
func TestABaseLoopFailsItsFiles(t *testing.T) {
	loaded, problems := writeThemeDir(t, map[string]string{
		"a.json":  `{"id": "loop_a", "base": "loop_b"}`,
		"b.json":  `{"id": "loop_b", "base": "loop_a"}`,
		"ok.json": `{"id": "loop_ok", "base": "nord"}`,
	})
	if !slices.Equal(loaded, []string{"loop_ok"}) {
		t.Errorf("loaded %v, want only loop_ok", loaded)
	}
	if len(problems) != 2 || !strings.Contains(strings.Join(problems, "\n"), "in a loop") {
		t.Errorf("problems %q do not say why both files failed", problems)
	}
}
