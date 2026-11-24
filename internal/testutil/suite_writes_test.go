package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
//
// CI runs the guard in two halves instead, so the suite runs once. With
// TUIOS_ISOLATION_SEED=DIR the test seeds DIR and records its checksums. The
// CI test steps then run with DIR as their home. With TUIOS_ISOLATION_VERIFY
// set to the same DIR, the test fails when the tree differs from the record.
func TestTheSuiteWritesNothingOutsideItsOwnTree(t *testing.T) {
	if dir := os.Getenv(seedEnv); dir != "" {
		seedAndRecord(t, dir)
		return
	}
	if dir := os.Getenv(verifyEnv); dir != "" {
		verifyRecord(t, dir)
		return
	}
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
	watched := watchedDirs(home)

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

// seedEnv and verifyEnv name the two halves of the guard that CI runs around
// its own test steps.
const (
	seedEnv   = "TUIOS_ISOLATION_SEED"
	verifyEnv = "TUIOS_ISOLATION_VERIFY"
)

// recordName is the file in the seeded home that holds the checksums. It sits
// outside the watched folders, so the record is not part of what it checks.
const recordName = "isolation-sums.txt"

// watchedDirs are the folders of home that the suite must leave as they are.
func watchedDirs(home string) []string {
	return []string{
		filepath.Join(home, "state"), filepath.Join(home, "config"),
		filepath.Join(home, "cache"), filepath.Join(home, "data"),
	}
}

// seedAndRecord seeds home as the guard does and writes the checksums of the
// watched folders to the record, one "sum path" line each.
func seedAndRecord(t *testing.T, home string) {
	t.Helper()
	seedUsedHome(t, home)
	sums, err := checksum(watchedDirs(home))
	if err != nil {
		t.Fatalf("checksum the seeded tree: %v", err)
	}
	var b strings.Builder
	for path, sum := range sums {
		b.WriteString(sum + " " + path + "\n")
	}
	if err := os.WriteFile(filepath.Join(home, recordName), []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write the record: %v", err)
	}
}

// verifyRecord fails when the watched folders of home differ from the record
// seedAndRecord wrote.
func verifyRecord(t *testing.T, home string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, recordName))
	if err != nil {
		t.Fatalf("read the record of the seeded tree: %v", err)
	}
	before := map[string]string{}
	for line := range strings.Lines(string(raw)) {
		sum, path, ok := strings.Cut(strings.TrimSuffix(line, "\n"), " ")
		if ok {
			before[path] = sum
		}
	}
	after, err := checksum(watchedDirs(home))
	if err != nil {
		t.Fatalf("checksum the seeded tree: %v", err)
	}
	if diff := diffDirs(before, after); diff != nil {
		t.Fatalf("the suite reached outside its own directories:\n%v", diff)
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
// packagesEnv names the packages the child suite tests, apart by spaces,
// instead of ./internal/... ./cmd/... ./pkg/....
const packagesEnv = "TUIOS_ISOLATION_PACKAGES"

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

	pkgs := []string{"./internal/...", "./cmd/...", "./pkg/..."}
	// CI names the packages a pull request can affect, so the guard runs
	// the tests that could have changed what they write.
	if list := strings.Fields(os.Getenv(packagesEnv)); len(list) > 0 {
		pkgs = list
	}
	split := []string{}
	for _, p := range []string{"internal/app", "internal/session"} {
		if slices.ContainsFunc(pkgs, func(q string) bool {
			return strings.HasSuffix(q, "/"+p) || q == "./"+p || strings.HasPrefix(q, "./internal/...") || strings.HasPrefix(q, "./...")
		}) {
			split = append(split, "./"+p)
		}
	}
	cmd := exec.Command("go", append([]string{"test", "-count=1"}, pkgs...)...)
	// internal/app and internal/session run their tests one after another
	// for over three minutes each, so the child suite took as long as the
	// rest of the unit tests together. scripts/go-test-lanes.sh splits them
	// across four processes. It needs bash 4 for its arrays, which macOS
	// does not ship, so plain go test stays the fallback.
	lanes := filepath.Join(root, "scripts", "go-test-lanes.sh")
	if _, err := os.Stat(lanes); err == nil && len(split) > 0 && exec.Command("bash", "-c", `[ "${BASH_VERSINFO[0]}" -ge 4 ]`).Run() == nil {
		cmd = exec.Command("bash", append([]string{lanes, "-count=1", "--"}, pkgs...)...)
		env = append(env,
			"SPLIT="+strings.Join(split, " "),
			"LOG="+filepath.Join(t.TempDir(), "lanes.log"),
		)
	}
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
