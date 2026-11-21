package tuie2e

import (
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

var guardDirs = userServiceDirs

func userServiceDirs() []string {
	home := ""
	if u, err := user.Current(); err == nil {
		home = u.HomeDir
	}
	if home == "" {
		return nil
	}
	switch runtime.GOOS {
	case "linux":
		dirs := []string{filepath.Join(home, ".config", "systemd", "user")}
		if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
			dirs = append(dirs, filepath.Join(x, "systemd", "user"))
		}
		return dirs
	case "darwin":
		return []string{filepath.Join(home, "Library", "LaunchAgents")}
	}
	return nil
}

type serviceSnapshot map[string]string

func snapshotUserServices() serviceSnapshot {
	snap := serviceSnapshot{}
	for _, dir := range guardDirs() {
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // a folder that is not there is a snapshot of nothing
			}
			info, err := os.Lstat(path)
			if err != nil {
				return nil //nolint:nilerr // gone between the walk and the stat
			}
			entry := fmt.Sprintf("%s %d %d", info.Mode(), info.Size(), info.ModTime().UnixNano())
			if info.Mode()&fs.ModeSymlink != 0 {
				target, _ := os.Readlink(path)
				entry += " -> " + target
			}
			if info.IsDir() {
				entry = info.Mode().String()
			}
			snap[path] = entry
			return nil
		})
	}
	return snap
}

func (s serviceSnapshot) changes() string {
	now := snapshotUserServices()
	var lines []string
	for path, was := range s {
		is, ok := now[path]
		switch {
		case !ok:
			lines = append(lines, "  removed "+path)
		case is != was:
			lines = append(lines, "  changed "+path)
		}
	}
	for path := range now {
		if _, ok := s[path]; !ok {
			lines = append(lines, "  added   "+path)
		}
	}
	slices.Sort(lines)
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// TestUserServicesGuardSeesAChange proves the guard on a folder of its own:
// a unit that is added, changed or removed shows in changes.
func TestUserServicesGuardSeesAChange(t *testing.T) {
	dir := t.TempDir()
	old := guardDirs
	guardDirs = func() []string { return []string{dir} }
	t.Cleanup(func() { guardDirs = old })
	unit := filepath.Join(dir, "tuios-daemon.service")
	if err := os.WriteFile(unit, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshotUserServices()
	if got := before.changes(); got != "" {
		t.Fatalf("no change, but the guard says:\n%s", got)
	}
	if err := os.WriteFile(unit, []byte("ab"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := before.changes(); !strings.Contains(got, "changed "+unit) {
		t.Errorf("a changed unit does not show:\n%s", got)
	}
	added := filepath.Join(dir, "other.service")
	if err := os.WriteFile(added, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := before.changes(); !strings.Contains(got, "added   "+added) {
		t.Errorf("an added unit does not show:\n%s", got)
	}
	if err := os.Remove(unit); err != nil {
		t.Fatal(err)
	}
	if got := before.changes(); !strings.Contains(got, "removed "+unit) {
		t.Errorf("a removed unit does not show:\n%s", got)
	}
}
