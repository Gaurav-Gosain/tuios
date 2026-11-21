package main

import (
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRewriteDeletesOnlyTheGuardSleeps(t *testing.T) {
	before, after := fixture(t, "modes.before.txt"), fixture(t, "modes.after.txt")
	got, sites, err := rewrite("x_test.go", before)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(after) {
		t.Fatalf("rewrite gave\n%s\nwant\n%s", got, after)
	}
	if len(sites) != 3 {
		t.Fatalf("got %d sites, want 3: %v", len(sites), sites)
	}
	again, sites, err := rewrite("x_test.go", got)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(got) || len(sites) != 0 {
		t.Fatalf("a second run changed %d sites", len(sites))
	}
}

func TestRewriteDropsTheTimeImportItLeavesUnused(t *testing.T) {
	before, after := fixture(t, "import.before.txt"), fixture(t, "import.after.txt")
	got, _, err := rewrite("y_test.go", before)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(after) {
		t.Fatalf("rewrite gave\n%s\nwant\n%s", got, after)
	}
}
