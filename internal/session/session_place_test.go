package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A session's place is what the rail labels an unnamed session with. These
// tests pin the three reads it is built from: the OSC 7 payload, the directory
// label, and the branch under .git, on layouts written by hand so no test needs
// a git identity to commit with.

func TestParseCwdReportAcceptsLocalPathsOnly(t *testing.T) {
	cases := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"file://localhost/home/u/dev/repo", "/home/u/dev/repo", true},
		{"file:///home/u/dev/repo", "/home/u/dev/repo", true},
		{"file://" + localHostname() + "/srv/x", "/srv/x", true},
		{"file://some-other-box/home/u", "", false},
		{"/a/b/../c", "/a/c", true},
		{"relative/dir", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := parseCwdReport(c.raw)
		if ok != c.ok || got != c.want {
			t.Errorf("parseCwdReport(%q) = %q, %v; want %q, %v", c.raw, got, ok, c.want, c.ok)
		}
	}
}

// writeFile creates a file with its parent directories.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestGitBranchReadsTheCheckoutLayouts reads HEAD the way git lays it out: a
// branch under .git, the same from a subdirectory, a worktree's .git file in the
// absolute form git writes and the relative form a submodule writes, and a
// detached HEAD as the short hash. A .git file naming no gitdir and a HEAD that
// is neither a ref nor a hash read as no branch.
func TestGitBranchReadsTheCheckoutLayouts(t *testing.T) {
	base := t.TempDir()
	main := filepath.Join(base, "main")
	writeFile(t, filepath.Join(main, ".git", "HEAD"), "ref: refs/heads/feat/labels\n")
	writeFile(t, filepath.Join(main, ".git", "worktrees", "wt", "HEAD"), "ref: refs/heads/wt-branch\n")
	deep := filepath.Join(main, "internal", "deep")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(base, "wt")
	writeFile(t, filepath.Join(wt, ".git"), "gitdir: "+filepath.Join(main, ".git", "worktrees", "wt")+"\n")
	sub := filepath.Join(base, "sub")
	writeFile(t, filepath.Join(sub, ".git"), "gitdir: ../main/.git/worktrees/wt\n")
	odd := filepath.Join(base, "odd")
	writeFile(t, filepath.Join(odd, ".git"), "not a checkout\n")
	detached := filepath.Join(base, "detached")
	writeFile(t, filepath.Join(detached, ".git", "HEAD"), "0123456789abcdef0123456789abcdef01234567\n")
	garbage := filepath.Join(base, "garbage")
	writeFile(t, filepath.Join(garbage, ".git", "HEAD"), "garbage\n")

	for _, tc := range []struct {
		name, dir, want string
	}{
		{"root", main, "feat/labels"},
		{"subdirectory", deep, "feat/labels"},
		{"absolute gitdir", wt, "wt-branch"},
		{"relative gitdir", sub, "wt-branch"},
		{"malformed .git file", odd, ""},
		{"detached HEAD", detached, "0123456"},
		{"unreadable HEAD", garbage, ""},
	} {
		if got := gitBranch(tc.dir); got != tc.want {
			t.Errorf("%s: gitBranch = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestGitBranchAgreesWithGitInit reads a checkout git itself made, so the
// hand-written layouts above cannot drift from the real format. It creates
// nothing but an empty repository and never commits, so it needs no identity.
func TestGitBranchAgreesWithGitInit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	repo := filepath.Join(t.TempDir(), "real")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(git, "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if out, err := exec.Command(git, "-C", repo, "symbolic-ref", "HEAD", "refs/heads/from-git").CombinedOutput(); err != nil {
		t.Fatalf("git symbolic-ref: %v: %s", err, out)
	}
	if got := gitBranch(repo); got != "from-git" {
		t.Fatalf("gitBranch on a git-made checkout = %q, want from-git", got)
	}
}
