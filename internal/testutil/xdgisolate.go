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
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/sound"
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

// isolateXDG points every XDG directory at a throwaway tree and returns that
// tree's path along with a function that removes it and reports whether the
// redirect was still in force when the run ended.
func isolateXDG() (dir string, check func() error) {
	tmp, err := os.MkdirTemp("", "tuios-test-xdg")
	if err != nil {
		panic(fmt.Sprintf("testutil: create XDG tree: %v", err))
	}
	for _, name := range xdgVars {
		if err := os.Setenv(name, tmp); err != nil {
			panic(fmt.Sprintf("testutil: set %s: %v", name, err))
		}
	}
	// HOME moves too, so a path built from os.UserHomeDir rather than from xdg
	// lands in the tree as well. internal/server derives an SSH host key that
	// way.
	if err := os.Setenv("HOME", tmp); err != nil {
		panic(fmt.Sprintf("testutil: set HOME: %v", err))
	}
	xdg.Reload()

	return tmp, func() error {
		defer func() { _ = os.RemoveAll(tmp) }()
		return stillRedirected(tmp)
	}
}

// stillRedirected reports any XDG base that no longer resolves inside the tree.
//
// The way back out is a test that redirects xdg for itself and reloads without
// restoring, which leaves the globals wherever the environment pointed at that
// moment for every test that follows. Three helpers had a version of this: they
// registered the restoring reload after t.Setenv, and cleanups run last in
// first out, so the reload went first and re-resolved onto the very temp
// directory it was meant to be leaving, moments before that directory was
// deleted.
func stillRedirected(tmp string) error {
	var escaped []string
	for name, path := range map[string]string{
		"XDG_CONFIG_HOME": xdg.ConfigHome,
		"XDG_DATA_HOME":   xdg.DataHome,
		"XDG_STATE_HOME":  xdg.StateHome,
		"XDG_CACHE_HOME":  xdg.CacheHome,
		"XDG_RUNTIME_DIR": xdg.RuntimeDir,
		"HOME":            xdg.Home,
	} {
		if !strings.HasPrefix(filepath.Clean(path)+string(filepath.Separator), filepath.Clean(tmp)+string(filepath.Separator)) {
			escaped = append(escaped, fmt.Sprintf("  %s is %s", name, path))
		}
	}
	if len(escaped) == 0 {
		return nil
	}
	sort.Strings(escaped)
	return fmt.Errorf("the run left these pointing outside its own tree %s, so the tests after it reached the developer's files:\n%s",
		tmp, strings.Join(escaped, "\n"))
}

// RunIsolated runs the package's tests against a throwaway XDG tree and
// returns the exit code to hand to os.Exit.
//
// Each setup function runs after the redirect and before the first test, and
// receives the tree's path. That is where a package seeds the fixture files its
// tests expect to find, so they read the fixture rather than whatever the
// developer happens to have.
func RunIsolated(m *testing.M, setup ...func(dir string)) int {
	// A test run must not reach the developer's speakers any more than it may
	// reach their config. The alert path plays a cue through a system audio
	// player, and a package that exercises it would otherwise make noise on
	// every `go test`, and spawn processes on a CI box that has no device.
	os.Setenv(sound.DisableEnv, "1") //nolint:errcheck,gosec // a failure here only means the tests are audible

	dir, check := isolateXDG()
	for _, fn := range setup {
		fn(dir)
	}
	code := m.Run()
	if err := check(); err != nil {
		fmt.Fprintf(os.Stderr, "\n%v\n", err)
		if code == 0 {
			code = 1
		}
	}
	return code
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
				// A socket, or a file removed mid-walk, is not evidence of a
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
	return fmt.Errorf("%s", strings.Join(lines, "\n"))
}
