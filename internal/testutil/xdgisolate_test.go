package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// subprocessHarness lists the packages that isolate by a different mechanism
// and so are held to it rather than to RunIsolated. They drive tuios as a real
// child process with the environment already set, which works where t.Setenv
// does not because the child resolves its paths at its own init, long after.
// They are named rather than detected, so adding one is a decision somebody
// makes rather than one a naming convention makes for them.
var subprocessHarness = map[string]bool{
	"e2e": true,
}

// TestEveryTestPackageIsolatesTheXDGTree is the cheap half of the guard, and
// the one that names the package at fault.
//
// A package whose TestMain does not isolate runs against the developer's own
// config and state: it writes into them, and it reads them, so it asserts
// against whatever that machine happens to hold. Neither shows up as a failure
// in the package doing it, which is why the requirement is checked from
// outside. Adding the first test to a new package now fails this until that
// package opts in, and that is the property a helper each test has to remember
// to call cannot have.
func TestEveryTestPackageIsolatesTheXDGTree(t *testing.T) {
	root := moduleRootFrom(t)

	var missing []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		switch name := d.Name(); {
		case path == root:
			return nil
		case name == "testdata" || strings.HasPrefix(name, "."):
			return filepath.SkipDir
		}
		// A directory carrying its own go.mod is a separate module with its own
		// arrangements, and its tests do not run in this binary's suite.
		if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(root, path)
		if !subprocessHarness[rel] && !isolatedOrTestFree(t, path) {
			missing = append(missing, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	for _, pkg := range missing {
		t.Errorf("%s has tests but no TestMain calling testutil.RunIsolated, so it runs against the developer's own XDG directories", pkg)
	}
}

// isolatedOrTestFree reports whether dir either has no tests or opts into
// isolation.
func isolatedOrTestFree(t *testing.T, dir string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var tests []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), "_test.go") {
			tests = append(tests, filepath.Join(dir, e.Name()))
		}
	}
	if len(tests) == 0 {
		return true
	}
	for _, path := range tests {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(src), "testutil.RunIsolated(") {
			return true
		}
	}
	return false
}

// TestIsolationReportsWhatTheRunTouched pins the reporting the guard depends
// on. A checker that returns clean whatever happened leaves exactly the hole it
// was written to close.
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
	for _, want := range []string{
		"created " + filepath.Join(dir, "created"),
		"modified " + edited,
		"removed " + kept,
	} {
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

// TestRedirectCheckCatchesAnEscapedGlobal covers stillRedirected, which is what
// each test binary checks for itself after its own run.
func TestRedirectCheckCatchesAnEscapedGlobal(t *testing.T) {
	tmp := t.TempDir()
	if err := stillRedirected(tmp); err == nil {
		t.Fatal("globals pointing at the real home were reported as redirected")
	}
	// The binary's own isolation is in force, so the tree it was given passes.
	if err := stillRedirected(os.Getenv("XDG_STATE_HOME")); err != nil {
		t.Fatalf("this binary's own tree was reported as escaped: %v", err)
	}
}
