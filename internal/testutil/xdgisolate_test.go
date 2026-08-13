package testutil_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryTestPackageIsolatesTheXDGTree is the guard the rest of this change
// exists to make possible.
//
// A package whose TestMain does not isolate runs against the developer's own
// config and state: it writes into them, and it reads them, so it asserts
// against whatever that machine happens to hold. That is how a fixture window
// id ended up coloured in a real sidebar.json, and it is not something the
// leaking package can be relied on to notice, since the leak is silent and the
// tests pass either way.
//
// So the requirement is checked from outside. Adding the first test to a
// package now fails this until that package opts in, which is the property a
// helper each test has to remember cannot have.
func TestEveryTestPackageIsolatesTheXDGTree(t *testing.T) {
	root := moduleRoot(t)

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
		case name == ".git" || name == "testdata" || strings.HasPrefix(name, "."):
			return filepath.SkipDir
		}
		// A directory carrying its own go.mod is a separate module with its own
		// arrangements; e2e/tui isolates per test by spawning tuios with the
		// environment already set, which works because the child resolves its
		// paths at its own init.
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

// subprocessHarness lists the packages that isolate by a different mechanism
// and so are held to it rather than to RunIsolated. They drive tuios as a real
// child process with the environment already set, which works where t.Setenv
// does not because the child resolves its paths at its own init, long after.
// They are named rather than detected, so adding a package here is a decision
// someone makes rather than one a naming convention makes for them.
var subprocessHarness = map[string]bool{
	"e2e": true,
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

// moduleRoot walks up from the package directory to the directory holding the
// module's go.mod, so the guard covers the whole module rather than whatever
// the tests happen to be run from.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the package directory")
		}
		dir = parent
	}
}
