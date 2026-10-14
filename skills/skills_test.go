package skills

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestArchiveMatchesFiles holds the embedded archive to the files under
// skills/tuios: every name `tuios --skill` takes prints the file's exact bytes,
// "all" prints them joined as before the archive, and no file is missing or
// extra. It fails when a skill file is edited without `go generate ./skills`.
func TestArchiveMatchesFiles(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("tuios", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	disk := map[string]string{}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		disk[strings.TrimSuffix(filepath.Base(p), ".md")] = string(data)
	}
	core, ok := disk["SKILL"]
	if !ok {
		t.Fatal("skills/tuios/SKILL.md is missing")
	}

	var topics []string
	for name := range disk {
		if name != "SKILL" {
			topics = append(topics, name)
		}
	}
	slices.Sort(topics)
	if got := TopicNames(); !slices.Equal(got, topics) {
		t.Fatalf("topics in the archive %v, files on disk %v; run go generate ./skills", got, topics)
	}

	check := func(name, want string) {
		t.Helper()
		got, err := Lookup(name)
		if err != nil {
			t.Fatalf("Lookup(%q): %v", name, err)
		}
		if got != want {
			t.Errorf("Lookup(%q) differs from the file on disk; run go generate ./skills", name)
		}
	}
	check("", core)
	check("core", core)
	for _, name := range topics {
		check(name, disk[name])
	}

	// "all" as the embed.FS version built it: the core, then each topic by
	// name, trailing newlines trimmed, a blank line between.
	parts := []string{strings.TrimRight(core, "\n")}
	for _, name := range topics {
		parts = append(parts, strings.TrimRight(disk[name], "\n"))
	}
	check("all", strings.Join(parts, "\n\n")+"\n")
}
