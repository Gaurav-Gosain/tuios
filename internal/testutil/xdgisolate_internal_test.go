package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIsolationReportsWhatTheRunTouched covers the check RunIsolated makes
// after the tests finish. A guard that quietly reported nothing would leave
// exactly the hole it was written to close, so the reporting is pinned rather
// than assumed.
func TestIsolationReportsWhatTheRunTouched(t *testing.T) {
	dir := t.TempDir()
	kept := filepath.Join(dir, "kept")
	edited := filepath.Join(dir, "edited")
	for _, p := range []string{kept, edited} {
		if err := os.WriteFile(p, []byte("before"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	before, err := hashDirs([]string{dir})
	if err != nil {
		t.Fatalf("hashDirs: %v", err)
	}
	if err := os.WriteFile(edited, []byte("after"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "created"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(kept); err != nil {
		t.Fatal(err)
	}

	after, err := hashDirs([]string{dir})
	if err != nil {
		t.Fatalf("hashDirs: %v", err)
	}
	report := diffDirs(before, after)
	if report == nil {
		t.Fatal("a run that created, changed and removed a file was reported as clean")
	}
	for _, want := range []string{"created " + filepath.Join(dir, "created"), "modified " + edited, "removed " + kept} {
		if !strings.Contains(report.Error(), want) {
			t.Errorf("report does not mention %q:\n%v", want, report)
		}
	}
}

// TestIsolationPassesAnUntouchedTree keeps the guard from failing every run for
// reasons of its own. A directory that does not exist is the ordinary case on a
// machine that has never run tuios, and it must read as clean.
func TestIsolationPassesAnUntouchedTree(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	absent := filepath.Join(dir, "never-created")

	before, err := hashDirs([]string{dir, absent})
	if err != nil {
		t.Fatalf("hashDirs: %v", err)
	}
	after, err := hashDirs([]string{dir, absent})
	if err != nil {
		t.Fatalf("hashDirs: %v", err)
	}
	if report := diffDirs(before, after); report != nil {
		t.Fatalf("an untouched tree was reported as dirty:\n%v", report)
	}
}
