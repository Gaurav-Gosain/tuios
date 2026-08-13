package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// childMarker stops the recursion. The child runs this package too, and
// without the marker it would launch a suite of its own, and so on.
const childMarker = "TUIOS_ISOLATION_CHILD"

// TestTheSuiteWritesNothingOutsideItsOwnTree is the guard this change exists to
// leave behind.
//
// It gives the whole suite a home of its own, seeded to look like a developer
// who has used tuios, runs it, and checksums the tree afterwards. A package
// that reaches a real path writes into that seeded tree instead, and the
// checksums say so.
//
// The tree is private, which is the point. The obvious cheaper design, having
// each test binary diff the developer's actual directories, was tried first and
// does not work: on a machine with tuios running, the live daemon rewrites its
// own session state while the tests run, and the check reports the daemon's
// writes as the suite's. A guard that fails for reasons the suite did not cause
// gets switched off, and then it guards nothing.
func TestTheSuiteWritesNothingOutsideItsOwnTree(t *testing.T) {
	if os.Getenv(childMarker) != "" {
		t.Skip("this is the child suite")
	}
	if testing.Short() {
		t.Skip("runs the whole suite a second time")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain to run the suite with")
	}

	home := t.TempDir()
	seedUsedHome(t, home)
	watched := []string{
		filepath.Join(home, "state"), filepath.Join(home, "config"),
		filepath.Join(home, "cache"), filepath.Join(home, "data"),
	}

	before, err := checksum(watched)
	if err != nil {
		t.Fatalf("checksum the seeded tree: %v", err)
	}

	out, err := runSuite(t, home)
	if err != nil {
		// A failing suite is somebody else's test to fix, and its writes are
		// still worth reporting, so this is not fatal on its own.
		t.Logf("the child suite did not pass, which this guard does not judge: %v", err)
	}

	after, err := checksum(watched)
	if err != nil {
		t.Fatalf("checksum the seeded tree: %v", err)
	}
	if diff := diffDirs(before, after); diff != nil {
		t.Fatalf("the suite reached outside its own directories:\n%v\n\nsuite output:\n%s", diff, tail(out))
	}
}

// checksum digests the watched tree, minus the go command's own counters. The
// toolchain writes those into XDG_CONFIG_HOME on every invocation, and they say
// nothing about what the test binaries did.
func checksum(dirs []string) (map[string]string, error) {
	sums, err := hashDirs(dirs)
	if err != nil {
		return nil, err
	}
	for path := range sums {
		if strings.Contains(path, string(filepath.Separator)+"go"+string(filepath.Separator)+"telemetry"+string(filepath.Separator)) {
			delete(sums, path)
		}
	}
	return sums, nil
}

// seedUsedHome fills the tree with the files a developer who has run tuios
// would have, so a write shows up as a changed checksum and not only as a new
// path. An empty tree would miss a test that overwrites a real file with the
// same shape, which is exactly what the sidebar state leak did.
func seedUsedHome(t *testing.T, home string) {
	t.Helper()
	files := map[string]string{
		"state/tuios/sidebar.json":   `{"width":28,"accent_colors":{"real-window":"#ff8800"}}`,
		"config/tuios/config.toml":   "[appearance]\ntheme = \"catppuccin-mocha\"\n",
		"data/tuios/tape-trust.toml": "\n",
		"cache/tuios/marker":         "\n",
	}
	for rel, body := range files {
		path := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(home, "run"), 0o700); err != nil {
		t.Fatal(err)
	}
}

// runSuite runs every package in the module against home as the whole of the
// developer's environment.
func runSuite(t *testing.T, home string) (string, error) {
	t.Helper()
	root := moduleRootFrom(t)

	// The build and module caches stay where they are. They are derived from
	// XDG_CACHE_HOME otherwise, which would fill the seeded tree with the
	// toolchain's own files and rebuild the world to do it.
	env := append(os.Environ(),
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, "config"),
		"XDG_DATA_HOME="+filepath.Join(home, "data"),
		"XDG_STATE_HOME="+filepath.Join(home, "state"),
		"XDG_CACHE_HOME="+filepath.Join(home, "cache"),
		"XDG_RUNTIME_DIR="+filepath.Join(home, "run"),
		"GOCACHE="+goEnv(t, "GOCACHE"),
		"GOMODCACHE="+goEnv(t, "GOMODCACHE"),
		childMarker+"=1",
	)

	cmd := exec.Command("go", "test", "-count=1", "./internal/...", "./cmd/...", "./pkg/...")
	cmd.Dir = root
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func goEnv(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.Command("go", "env", name).Output()
	if err != nil {
		t.Fatalf("go env %s: %v", name, err)
	}
	return strings.TrimSpace(string(out))
}

// moduleRootFrom walks up from the package directory to the module's go.mod.
func moduleRootFrom(t *testing.T) string {
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

// tail keeps a failure report readable when the child suite printed a lot.
func tail(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) > 40 {
		lines = lines[len(lines)-40:]
	}
	return strings.Join(lines, "\n")
}
