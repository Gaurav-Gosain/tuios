package app_test

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
)

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

func tapeDir(t *testing.T) string {
	t.Helper()
	dir, err := app.GetTapeDirectory()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

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
