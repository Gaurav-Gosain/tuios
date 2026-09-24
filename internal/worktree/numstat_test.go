package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

// TestDiffWorkingComparesTwoAttempts: the diff runs from the first worktree's
// working state to the second's, uncommitted files included.
func TestDiffWorkingComparesTwoAttempts(t *testing.T) {
	repo := testutil.GitRepo(t)
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	if _, err := Add(repo, a, "a", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(repo, b, "b", "main"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a, "only-a.txt"), []byte("from a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, "only-b.txt"), []byte("from b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diff, err := DiffWorking(context.Background(), a, b, false)
	if err != nil {
		t.Fatalf("DiffWorking: %v", err)
	}
	if !strings.Contains(diff, "+from b") || !strings.Contains(diff, "-from a") {
		t.Errorf("the diff does not run from a to b:\n%s", diff)
	}
	stat, err := DiffWorking(context.Background(), a, b, true)
	if err != nil || !strings.Contains(stat, "2 files changed") {
		t.Errorf("stat = %q, %v; want the two files", stat, err)
	}
}
