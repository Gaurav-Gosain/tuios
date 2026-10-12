//go:build windows

package herdrplugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadDropsTheExtendedPrefix loads a manifest by the \\?\ path herdr
// writes to plugins.json. The plugin's commands run in PluginRoot, and a
// Node plugin started in a \\?\ folder cannot resolve its own script
// (issue #654), so the root must come back as a plain drive path.
func TestLoadDropsTheExtendedPrefix(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, ManifestName)
	body := "id = \"test.prefix\"\nname = \"Prefix\"\nversion = \"0.1.0\"\nmin_herdr_version = \"0.9.0\"\n"
	if err := os.WriteFile(manifest, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p, perr := Load(`\\?\` + manifest)
	if perr != nil {
		t.Fatalf("Load: %s", perr.Msg)
	}
	for name, got := range map[string]string{"PluginRoot": p.PluginRoot, "ManifestPath": p.ManifestPath} {
		if strings.HasPrefix(got, `\\?\`) {
			t.Errorf("%s = %q, want it without the \\\\?\\ prefix", name, got)
		}
	}
}
