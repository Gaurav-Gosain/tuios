package app_test

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// The machine a screen test's lists read from: programs on PATH for the
// launcher, tape files for the tape manager.

// programsOnPath gives the launcher three programs to list and nothing else.
func programsOnPath(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"aa-prog", "bb-prog", "cc-prog"} {
		writeFile(t, filepath.Join(dir, name), "#!/bin/sh\n")
		if err := os.Chmod(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

// tapeDir is where the tape manager looks for tapes.
func tapeDir(t *testing.T) string {
	t.Helper()
	dir, err := app.GetTapeDirectory()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// tapeFiles puts three tapes where the tape manager looks, and takes them
// away afterwards: the directory is the test binary's whole XDG tree.
func tapeFiles(t *testing.T) {
	t.Helper()
	dir := tapeDir(t)
	for _, name := range []string{"a.tape", "b.tape", "c.tape"} {
		writeFile(t, filepath.Join(dir, name), "Type \"x\"\n")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
}

func itoa(n int) string { return strconv.Itoa(n) }
