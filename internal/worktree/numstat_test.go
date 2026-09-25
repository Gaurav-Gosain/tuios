package worktree

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

func TestParseNumstatCountsFilesLinesAndBinaries(t *testing.T) {
	out := "3\t1\ta.go\n-\t-\tlogo.png\n10\t0\tdocs/{old => new}.md\r\n\n"
	got := ParseNumstat(out)
	want := Numstat{Files: 3, Added: 13, Removed: 1}
	if got != want {
		t.Errorf("ParseNumstat = %+v, want %+v", got, want)
	}
	if got := ParseNumstat(""); got != (Numstat{}) {
		t.Errorf("ParseNumstat of nothing = %+v, want zero", got)
	}
}

// TestWorkingNumstatCountsCommitsAndUncommittedWork: the count covers what
// the worktree committed, what it changed and did not commit, and what it
// added and did not stage, and leaves out what git ignores. The worktree's
// own index is not touched.
func TestWorkingNumstatCountsCommitsAndUncommittedWork(t *testing.T) {
	repo := testutil.GitRepo(t)
	path := filepath.Join(t.TempDir(), "wt")
	if _, err := Add(repo, path, "x", "main"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(path, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("README", "hello\ncommitted\n")
	testutil.Git(t, path, "commit", "-q", "-am", "change")
	write("README", "hello\ncommitted\nuncommitted\n")
	write("new.txt", "one\ntwo\n")
	write(".gitignore", "ignored.log\n")
	write("ignored.log", "noise\n")
	before := testutil.Git(t, path, "status", "--porcelain")

	n, err := WorkingNumstat(context.Background(), path, "main")
	if err != nil {
		t.Fatalf("WorkingNumstat: %v", err)
	}
	// README +2, new.txt +2, .gitignore +1. ignored.log is not counted.
	if want := (Numstat{Files: 3, Added: 5, Removed: 0}); n != want {
		t.Errorf("WorkingNumstat = %+v, want %+v", n, want)
	}
	if after := testutil.Git(t, path, "status", "--porcelain"); after != before {
		t.Errorf("git status changed:\nbefore %q\nafter  %q", before, after)
	}
	if _, err := WorkingNumstat(context.Background(), path, "--output=/tmp/x"); err == nil {
		t.Error("a base that reads as an option was accepted")
	}
}

// TestSnapshotTreeStartsFromTheWorktreeIndex: the temporary index is a copy of
// the worktree's own, so a tracked file whose stat data matches the index is
// not hashed again. The test makes one file that only a rehash can tell apart
// from what the index says: new content of the same size, with the old
// modification time put back. A snapshot built from an empty index reads the
// new content; one built from the copy trusts the stat cache, as git status
// does.
func TestSnapshotTreeStartsFromTheWorktreeIndex(t *testing.T) {
	repo := testutil.GitRepo(t)
	path := filepath.Join(t.TempDir(), "wt")
	if _, err := Add(repo, path, "x", "main"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	file := filepath.Join(path, "cached.txt")
	if err := os.WriteFile(file, []byte("aaaa\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, path, "add", "cached.txt")
	testutil.Git(t, path, "commit", "-q", "-m", "cached")
	// The index now holds the file's stat data, written after the file's
	// modification time, so git trusts it.
	testutil.Git(t, path, "update-index", "--refresh")
	want := testutil.Git(t, path, "rev-parse", "HEAD^{tree}")

	if err := os.WriteFile(file, []byte("bbbb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	if st := testutil.Git(t, path, "status", "--porcelain"); st != "" {
		t.Skipf("git rehashed the file itself (status %q), so the stat cache cannot be observed here", st)
	}
	got, err := SnapshotTree(context.Background(), path)
	if err != nil {
		t.Fatalf("SnapshotTree: %v", err)
	}
	if got != want {
		t.Errorf("SnapshotTree = %s, want HEAD's tree %s: the file was hashed again, so the worktree's index was not used", got, want)
	}
}
