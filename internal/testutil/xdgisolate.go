package testutil

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/sound"
	"github.com/adrg/xdg"
)

var xdgVars = []string{
	"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME",
	"XDG_CACHE_HOME", "XDG_RUNTIME_DIR",
}

var windowsDirVars = []string{"USERPROFILE", "LOCALAPPDATA", "APPDATA"}

func isolateXDG() (dir string, check func() error) {
	tmp, err := os.MkdirTemp("", "tuios-test-xdg")
	if err != nil {
		panic(fmt.Sprintf("testutil: create XDG tree: %v", err))
	}
	pinGoDirs()
	for _, name := range xdgVars {
		if err := os.Setenv(name, tmp); err != nil {
			panic(fmt.Sprintf("testutil: set %s: %v", name, err))
		}
	}
	if err := os.Setenv("HOME", tmp); err != nil {
		panic(fmt.Sprintf("testutil: set HOME: %v", err))
	}
	if runtime.GOOS == "windows" {
		for _, name := range windowsDirVars {
			if err := os.Setenv(name, tmp); err != nil {
				panic(fmt.Sprintf("testutil: set %s: %v", name, err))
			}
		}
	}
	if os.Getenv(isolatedEnv) == "" {
		ClearPaneEnv()
	}
	if err := os.Setenv(isolatedEnv, "1"); err != nil {
		panic(fmt.Sprintf("testutil: set %s: %v", isolatedEnv, err))
	}
	noOmarchy := filepath.Join(tmp, "omarchy-current")
	if err := os.MkdirAll(noOmarchy, 0o755); err != nil {
		panic(fmt.Sprintf("testutil: create %s: %v", noOmarchy, err))
	}
	if err := os.Setenv("SWATCH_OMARCHY_DIR", noOmarchy); err != nil {
		panic(fmt.Sprintf("testutil: set SWATCH_OMARCHY_DIR: %v", err))
	}
	if err := os.Unsetenv("OMARCHY_PATH"); err != nil {
		panic(fmt.Sprintf("testutil: unset OMARCHY_PATH: %v", err))
	}
	xdg.Reload()

	return tmp, func() error {
		defer func() { _ = os.RemoveAll(tmp) }()
		return stillRedirected(tmp)
	}
}

var paneEnvVars = []string{
	"TUIOS_SOCKET", "TUIOS_SERVER", "TUIOS_SESSION", "TUIOS_SESSION_REMOTE",
	"TUIOS_PANE_ID", "TUIOS_WINDOW_ID", "TUIOS_WINDOW_NAME", "TUIOS_PANE_TOKEN",
	"TUIOS_PANE_GRANTS", "TUIOS_PANE_HOSTED", "TUIOS_PANE_TTY", "TUIOS_RESTORED",
	"TUIOS_HOST", "TUIOS_ENV",
}

const isolatedEnv = "TUIOS_TEST_ISOLATED"

// ClearPaneEnv unsets every variable a tuios pane gives the programs in it,
// so a test run from a pane sees the environment CI does. RunIsolated calls
// it, and so does the e2e suite's TestMain, which does not use RunIsolated.
func ClearPaneEnv() {
	for _, name := range paneEnvVars {
		if err := os.Unsetenv(name); err != nil {
			panic(fmt.Sprintf("testutil: unset %s: %v", name, err))
		}
	}
}

func pinGoDirs() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		gopath = filepath.Join(home, "go")
		_ = os.Setenv("GOPATH", gopath)
	}
	if os.Getenv("GOMODCACHE") == "" {
		first, _, _ := strings.Cut(gopath, string(os.PathListSeparator))
		_ = os.Setenv("GOMODCACHE", filepath.Join(first, "pkg", "mod"))
	}
	if os.Getenv("GOCACHE") == "" {
		if cache, err := os.UserCacheDir(); err == nil {
			_ = os.Setenv("GOCACHE", filepath.Join(cache, "go-build"))
		}
	}
}

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
		if name == "HOME" && runtime.GOOS == "windows" {
			continue
		}
		if !strings.HasPrefix(filepath.Clean(path)+string(filepath.Separator), filepath.Clean(tmp)+string(filepath.Separator)) {
			escaped = append(escaped, fmt.Sprintf("  %s is %s", name, path))
		}
	}
	if runtime.GOOS == "windows" {
		for _, name := range windowsDirVars {
			path := os.Getenv(name)
			if !strings.HasPrefix(filepath.Clean(path)+string(filepath.Separator), filepath.Clean(tmp)+string(filepath.Separator)) {
				escaped = append(escaped, fmt.Sprintf("  %s is %s", name, path))
			}
		}
	}
	if len(escaped) == 0 {
		return nil
	}
	sort.Strings(escaped)
	return errfmt.Errorf("the run left these pointing outside its own tree %s, so the tests after it reached the developer's files:\n%s",
		tmp, strings.Join(escaped, "\n"))
}

const watcherSettle = 3 * time.Second

func settledWatchers(count func() int, start int, wait time.Duration) int {
	deadline := time.Now().Add(wait)
	for {
		left := count() - start
		if left <= 0 || !time.Now().Before(deadline) {
			return left
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// RunIsolated runs the package's tests against a throwaway XDG tree and
// returns the exit code to hand to os.Exit.
//
// Each setup function runs after the redirect and before the first test, and
// receives the tree's path. That is where a package seeds the fixture files its
// tests expect to find, so they read the fixture rather than whatever the
// developer happens to have.
func RunIsolated(m *testing.M, setup ...func(dir string)) int {
	os.Setenv(sound.DisableEnv, "1") //nolint:errcheck,gosec // a failure here only means the tests are audible

	dir, check := isolateXDG()
	for _, fn := range setup {
		fn(dir)
	}
	startPoller()
	watchers := watcherInstances()
	code := m.Run()
	if left := settledWatchers(watcherInstances, watchers, watcherSettle); left > 0 {
		fmt.Fprintf(os.Stderr, "\nthe tests left %d %s open (%d at the start, %d at the end): "+
			"a watcher was opened and never closed\n", left, watcherKind, watchers, watchers+left)
		if code == 0 {
			code = 1
		}
	}
	if err := check(); err != nil {
		fmt.Fprintf(os.Stderr, "\n%v\n", err)
		if code == 0 {
			code = 1
		}
	}
	return code
}

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
	return errfmt.Errorf("%s", strings.Join(lines, "\n"))
}
