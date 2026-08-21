package applist

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"
)

// binName gives a file the name the platform needs for executable to say yes.
func binName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func writeExec(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, binName(name))
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func writePlain(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func names(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name
	}
	return out
}

func TestScanKeepsOnlyExecutables(t *testing.T) {
	dir := t.TempDir()
	writeExec(t, dir, "runnable")
	writePlain(t, dir, "readme.txt")
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := names(Scan([]string{dir}))
	want := []string{binName("runnable")}
	if !slices.Equal(got, want) {
		t.Fatalf("Scan = %v, want %v", got, want)
	}
}

// TestScanFirstDirWins is the rule that keeps the launcher honest: it must
// resolve a name the way the shell would, so a program shadowed earlier in
// $PATH never appears under the shadowing name.
func TestScanFirstDirWins(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	wantPath := writeExec(t, first, "tool")
	writeExec(t, second, "tool")
	writeExec(t, second, "other")

	got := Scan([]string{first, second})
	if len(got) != 2 {
		t.Fatalf("Scan = %v, want 2 entries", names(got))
	}
	var tool *Entry
	for i := range got {
		if got[i].Name == binName("tool") {
			tool = &got[i]
		}
	}
	if tool == nil {
		t.Fatalf("Scan = %v, missing tool", names(got))
	}
	if tool.Path != wantPath {
		t.Errorf("tool resolved to %q, want the first $PATH entry %q", tool.Path, wantPath)
	}
	if tool.Dir != first {
		t.Errorf("tool.Dir = %q, want %q", tool.Dir, first)
	}
	if tool.Source != SourcePath {
		t.Errorf("tool.Source = %q, want %q", tool.Source, SourcePath)
	}
}

func TestScanSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs a privilege this test cannot assume on Windows")
	}
	target := t.TempDir()
	dir := t.TempDir()
	real := writeExec(t, target, "real")

	if err := os.Symlink(real, filepath.Join(dir, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(target, "gone"), filepath.Join(dir, "dangling")); err != nil {
		t.Fatal(err)
	}

	got := names(Scan([]string{dir}))
	if !slices.Equal(got, []string{"linked"}) {
		t.Fatalf("Scan = %v, want a live symlink kept and a dangling one dropped", got)
	}
}

func TestScanSkipsUnreadableDir(t *testing.T) {
	dir := t.TempDir()
	writeExec(t, dir, "here")
	got := names(Scan([]string{filepath.Join(dir, "nope"), dir}))
	if !slices.Equal(got, []string{binName("here")}) {
		t.Fatalf("Scan = %v, want the missing directory to be no obstacle", got)
	}
}

func TestSplitPathDropsRelativeAndDuplicate(t *testing.T) {
	sep := string(os.PathListSeparator)
	abs := t.TempDir()
	got := splitPath(abs + sep + "" + sep + "relative/bin" + sep + abs)
	if !slices.Equal(got, []string{abs}) {
		t.Fatalf("splitPath = %v, want just %q", got, abs)
	}
}

// TestCacheReusesUnchangedDirs is the promise that reopening the launcher is
// instant: a directory whose mtime has not moved must not be re-read. The proof
// is a file added without disturbing the recorded mtime, which the cache is then
// expected to miss.
func TestCacheReusesUnchangedDirs(t *testing.T) {
	dir := t.TempDir()
	writeExec(t, dir, "first")

	c := NewCache()
	got, changed := c.refresh([]string{dir})
	if !changed || !slices.Equal(names(got), []string{binName("first")}) {
		t.Fatalf("first refresh = %v changed=%v", names(got), changed)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeExec(t, dir, "second")
	// Restore the directory's mtime so the cache has no reason to look again.
	if err := os.Chtimes(dir, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}

	got, changed = c.refresh([]string{dir})
	if changed {
		t.Error("refresh rescanned a directory whose mtime had not moved")
	}
	if !slices.Equal(names(got), []string{binName("first")}) {
		t.Fatalf("cached refresh = %v, want the earlier listing", names(got))
	}
}

// TestCacheSeesNewProgram is the other half: a program installed while tuios is
// running has to show up without a restart.
func TestCacheSeesNewProgram(t *testing.T) {
	dir := t.TempDir()
	writeExec(t, dir, "first")

	c := NewCache()
	c.refresh([]string{dir})

	// Push the recorded mtime back so the new file is guaranteed to land on a
	// different one even where the filesystem has coarse timestamps.
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	c.dirs[dir] = cachedDir{mtime: old, entries: c.dirs[dir].entries}
	writeExec(t, dir, "second")

	got, changed := c.refresh([]string{dir})
	if !changed {
		t.Fatal("refresh missed a directory whose mtime moved")
	}
	if !slices.Contains(names(got), binName("second")) {
		t.Fatalf("refresh = %v, want the newly installed program", names(got))
	}
	if entries := names(c.Entries()); !slices.Contains(entries, binName("second")) {
		t.Fatalf("Entries = %v, want the refreshed list", entries)
	}
}

// TestCacheForgetsRemovedDir guards the case a cached listing would happily
// outlive: a $PATH entry that no longer exists must stop contributing.
func TestCacheForgetsRemovedDir(t *testing.T) {
	dir := t.TempDir()
	writeExec(t, dir, "doomed")

	c := NewCache()
	c.refresh([]string{dir})
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	got, _ := c.refresh([]string{dir})
	if len(got) != 0 {
		t.Fatalf("refresh = %v, want nothing from a directory that is gone", names(got))
	}
}

func TestEntriesBeforeFirstRefresh(t *testing.T) {
	if got := NewCache().Entries(); got != nil {
		t.Fatalf("Entries = %v before any scan, want nil", got)
	}
}
