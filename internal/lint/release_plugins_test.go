package lint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestReleasePluginsShipTheirPrograms checks each plugin that tuios plugins
// install takes by name against the release. A plugin of the release runs no
// build command, so a plugin that names a program must get it from a release
// archive: a goreleaser build of that binary, and an archive whose name is
// the one internal/release.AssetName asks for. Without them the install
// writes a plugin whose command does not exist.
//
// Negative control: drop the tuios-ci build or the tuios-ci-archive from
// .goreleaser.yml, or rename the archive's name_template, and this fails.
func TestReleasePluginsShipTheirPrograms(t *testing.T) {
	root := repoRoot(t)
	read := func(rel string) string {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	src := read("cmd/tuios/plugins_release.go")
	start := strings.Index(src, "var firstParty = map[string]firstPartyPlugin{")
	if start < 0 {
		t.Fatal("cmd/tuios/plugins_release.go: no firstParty map; the check no longer reads the file right")
	}
	body, _, _ := strings.Cut(src[start:], "\n}\n")
	entry := regexp.MustCompile(`"([a-z0-9-]+)":\s*\{(?:binary:\s*"([^"]+)")?\}`)
	entries := entry.FindAllStringSubmatch(body, -1)
	if len(entries) == 0 {
		t.Fatal("cmd/tuios/plugins_release.go: no entries in firstParty; the check no longer reads the file right")
	}

	blocks := strings.Split(read(".goreleaser.yml"), "\n  - id: ")
	binaries := 0
	for _, e := range entries {
		name, binary := e[1], e[2]
		if _, err := os.Stat(filepath.Join(root, "plugins", name, "tuios-plugin.toml")); err != nil {
			t.Errorf("firstParty names %s, but plugins/%s/tuios-plugin.toml does not exist", name, name)
		}
		if binary == "" {
			continue
		}
		binaries++
		manifest := read(filepath.Join("plugins", name, "tuios-plugin.toml"))
		if !strings.Contains(manifest, "bin/"+binary) {
			t.Errorf("plugins/%s/tuios-plugin.toml names no bin/%s, the program the release gives it", name, binary)
		}
		build := ""
		for _, b := range blocks {
			if strings.Contains(b, "\n    binary: "+binary+"\n") {
				build, _, _ = strings.Cut(b, "\n")
			}
		}
		if build == "" {
			t.Errorf(".goreleaser.yml has no build of binary %s for the %s plugin", binary, name)
			continue
		}
		archived := false
		for _, b := range blocks {
			if strings.Contains(b, "name_template: >-\n      "+binary+"_\n") && strings.Contains(b, "\n    ids:\n      - "+build+"\n") {
				archived = true
			}
		}
		if !archived {
			t.Errorf(".goreleaser.yml has no archive named %s_VERSION_OS_ARCH of build %s for the %s plugin", binary, build, name)
		}
	}
	if binaries == 0 {
		t.Error("firstParty names no plugin with a program; the check no longer reads the file right")
	}
}
