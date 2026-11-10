package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestListProgramsNamesThePath: list-programs answers the programs on the
// PATH of the daemon's machine, for a launcher (guios's Run a program, parity
// gap 18). A program in a scratch folder first on the daemon's PATH is
// listed by name with its path, and a file there that cannot run is not.
//
// How this could pass wrongly, written down first:
//   - The program could be found on some other PATH folder: its name is made
//     up for the test, and the path must be the scratch folder's.
//   - The verb could list every file: the scratch folder holds one that is
//     not executable, which must be left out.
func TestListProgramsNamesThePath(t *testing.T) {
	base := t.TempDir()
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	prog := filepath.Join(bin, "tuios-e2e-listed")
	if err := os.WriteFile(prog, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "tuios-e2e-notexec"), []byte("data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "progs", "--detach"); err != nil {
		t.Fatalf("new: %v: %s", err, out)
	}
	out, err := tuiosCLI(t, base, "call", "list-programs", `{"session":"progs"}`)
	if err != nil {
		t.Fatalf("list-programs: %v\n%s", err, out)
	}
	var res struct {
		Programs []struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"programs"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("read list-programs: %v\n%s", err, out)
	}
	found := ""
	for _, p := range res.Programs {
		switch p.Name {
		case "tuios-e2e-listed":
			found = p.Path
		case "tuios-e2e-notexec":
			t.Fatalf("list-programs listed a file that cannot run: %+v", p)
		}
	}
	if found != prog {
		t.Fatalf("list-programs gave tuios-e2e-listed at %q, want %q (%d programs)", found, prog, len(res.Programs))
	}
	_ = os.WriteFile(filepath.Join(artifactDir(t), "list-programs.txt"), []byte(out), 0o600)
}
