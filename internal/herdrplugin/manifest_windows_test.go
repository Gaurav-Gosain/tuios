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

// TestSamePathIgnoresTheExtendedPrefix is what tuios plugins unlink relies
// on: a dirs entry written with \\?\ and the plain path of the same folder
// are one plugin, so unlink by either form removes the entry.
func TestSamePathIgnoresTheExtendedPrefix(t *testing.T) {
	dir := t.TempDir()
	for _, pair := range [][2]string{
		{`\\?\` + dir, dir},
		{dir, `\\?\` + dir},
		{`\\?\` + filepath.Join(dir, ManifestName), filepath.Join(dir, ManifestName)},
	} {
		if !SamePath(pair[0], pair[1]) {
			t.Errorf("SamePath(%q, %q) = false, want true", pair[0], pair[1])
		}
	}
	if SamePath(`\\?\`+dir, filepath.Join(dir, "other")) {
		t.Errorf("SamePath matched two different folders")
	}
}
