package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"tuios.dev/swatch/omarchy"
)

// omarchyChildEnv marks the run of this test binary that checks the theme.
const omarchyChildEnv = "TUIOS_TEST_OMARCHY_CHILD"

// TestTheDevelopersOmarchyThemeStaysOutside runs this package's test binary
// again on a machine that looks like Omarchy: an applied theme under HOME and
// installed themes under OMARCHY_PATH. The child's TestMain must hide both
// before anything reads the theme, so no test runs in the developer's colours
// (#1492). The rehydration matrix and the depth test failed that way, but
// only on a machine with Omarchy, so CI never saw it.
//
// Ways this could pass wrongly, written down first:
//   - The fixture could not be what Omarchy reads. The parent loads it with
//     the same swatch source the app uses before it starts the child.
//   - The child could not run the check. It prints a marker the parent wants.
//   - TestMain could apply the looks before the redirect, as it did, and the
//     theme would be in force while the files look hidden. The child checks
//     the active theme as well as the list.
func TestTheDevelopersOmarchyThemeStaysOutside(t *testing.T) {
	if os.Getenv(omarchyChildEnv) == "1" {
		omarchyChild(t)
		return
	}
	fixture, err := filepath.Abs(filepath.Join("..", "..", "swatch", "omarchy", "testdata"))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	current := filepath.Join(home, ".local", "state", "omarchy", "current")
	if err := os.CopyFS(current, os.DirFS(filepath.Join(fixture, "current"))); err != nil {
		t.Fatalf("plant the applied theme: %v", err)
	}
	if _, err := omarchy.At(current).Load(); err != nil {
		t.Fatalf("the planted theme does not load, so the child would prove nothing: %v", err)
	}
	if len(omarchy.ThemesIn(filepath.Join(fixture, "themes"))) == 0 {
		t.Fatal("the fixture holds no installed theme, so the child would prove nothing")
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestTheDevelopersOmarchyThemeStaysOutside$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(),
		omarchyChildEnv+"=1",
		"HOME="+home,
		"OMARCHY_PATH="+fixture,
		"SWATCH_OMARCHY_DIR=",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the child run failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "OMARCHY-CHILD-CHECKED") {
		t.Fatalf("the child never ran the check:\n%s", out)
	}
}

// omarchyChild is the check, run inside the child.
func omarchyChild(t *testing.T) {
	if id := theme.CurrentThemeID(); id == theme.OmarchyID {
		t.Errorf("ASSERTION: the active theme is %q, the developer's applied Omarchy theme", id)
	}
	if theme.OmarchyInstalled() {
		t.Errorf("ASSERTION: the tests see an applied Omarchy theme")
	}
	for _, id := range theme.AvailableThemes() {
		if id == theme.OmarchyID || strings.HasPrefix(id, theme.OmarchyID+"/") {
			t.Errorf("ASSERTION: the theme list holds %q from the developer's Omarchy install", id)
		}
	}
	t.Log("OMARCHY-CHILD-CHECKED")
}
