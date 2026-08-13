package testutil

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/adrg/xdg"
)

// Test binaries must not touch the developer's own XDG directories. The xdg
// package resolves its base paths once at package init, so t.Setenv inside a
// test comes far too late to redirect anything: by then xdg.StateHome already
// holds the real ~/.local/state. Redirecting has to happen before the first
// test runs and it has to cover the whole binary, which is what TestMain is
// for. A per-test helper is the wrong shape for this, because the next test
// written is the one that forgets to call it.

// xdgVars are every base directory the app resolves paths from. All of them
// point at the same throwaway tree; the app namespaces itself under "tuios"
// inside each, so sharing a root loses nothing and keeps cleanup to one call.
var xdgVars = []string{
	"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME",
	"XDG_CACHE_HOME", "XDG_RUNTIME_DIR",
}

// appDir is the subdirectory the app owns inside each base directory. The
// escape check watches these and nothing else, so unrelated churn in the
// developer's ~/.cache cannot make a test flake.
const appDir = "tuios"

// isolateXDG points every XDG directory at a throwaway tree and returns that
// tree's path along with a function that removes it and reports whether the
// run reached the developer's real directories anyway.
func isolateXDG() (dir string, check func() error) {
	real := realAppDirs()
	before, err := hashDirs(real)
	if err != nil {
		panic(fmt.Sprintf("testutil: snapshot real XDG dirs: %v", err))
	}

	tmp, err := os.MkdirTemp("", "tuios-test-xdg")
	if err != nil {
		panic(fmt.Sprintf("testutil: create XDG tree: %v", err))
	}
	for _, name := range xdgVars {
		if err := os.Setenv(name, tmp); err != nil {
			panic(fmt.Sprintf("testutil: set %s: %v", name, err))
		}
	}
	// Also move HOME, so a path built from os.UserHomeDir rather than xdg
	// lands in the tree too.
	if err := os.Setenv("HOME", tmp); err != nil {
		panic(fmt.Sprintf("testutil: set HOME: %v", err))
	}
	xdg.Reload()

	return tmp, func() error {
		defer func() { _ = os.RemoveAll(tmp) }()
		after, err := hashDirs(real)
		if err != nil {
			return err
		}
		return diffDirs(before, after)
	}
}

// RunIsolated runs the package's tests against a throwaway XDG tree and
// returns the exit code to hand to os.Exit. A run that wrote into the
// developer's real directories fails, whatever path it took to get there, so a
// write site added later cannot reintroduce the leak quietly.
//
// Each setup function runs after the redirect and before the first test, and
// receives the tree's path. That is where a package seeds the fixture files its
// tests expect to find, so they read the fixture rather than whatever the
// developer happens to have.
func RunIsolated(m *testing.M, setup ...func(dir string)) int {
	dir, check := isolateXDG()
	for _, fn := range setup {
		fn(dir)
	}
	code := m.Run()
	if err := check(); err != nil {
		fmt.Fprintf(os.Stderr, "\nescaped the test XDG tree and wrote the developer's own files:\n%v\n", err)
		if code == 0 {
			code = 1
		}
	}
	return code
}

// realAppDirs returns the app's directory inside each of the developer's real
// base directories. It must be called before the redirect, while the xdg
// package still holds the values it resolved at init.
func realAppDirs() []string {
	bases := []string{xdg.ConfigHome, xdg.DataHome, xdg.StateHome, xdg.CacheHome, xdg.RuntimeDir}
	seen := make(map[string]bool, len(bases))
	out := make([]string, 0, len(bases))
	for _, b := range bases {
		if b == "" {
			continue
		}
		d := filepath.Join(b, appDir)
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// hashDirs maps every file under the given directories to a digest of its
// contents. A directory that does not exist contributes nothing, so a run that
// creates one from scratch shows up as new entries.
func hashDirs(dirs []string) (map[string]string, error) {
	out := map[string]string{}
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			sum, err := hashFile(path)
			if err != nil {
				// A socket or a file removed mid-walk is not evidence of a
				// write, and reporting it would make the check flaky.
				return nil //nolint:nilerr // unreadable entries are not evidence
			}
			out[path] = sum
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return out, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// diffDirs reports the files the run added, changed or removed.
func diffDirs(before, after map[string]string) error {
	var lines []string
	for path, sum := range after {
		switch prev, ok := before[path]; {
		case !ok:
			lines = append(lines, "  created "+path)
		case prev != sum:
			lines = append(lines, "  modified "+path)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			lines = append(lines, "  removed "+path)
		}
	}
	if len(lines) == 0 {
		return nil
	}
	sort.Strings(lines)
	msg := lines[0]
	for _, l := range lines[1:] {
		msg += "\n" + l
	}
	return fmt.Errorf("%s", msg)
}
