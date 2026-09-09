package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestDirLabelIsTheBaseNameWithTildeForHome(t *testing.T) {
	cases := []struct{ cwd, home, want string }{
		{"/home/u", "/home/u", "~"},
		{"/home/u/dev/repo", "/home/u", "repo"},
		{"/", "/home/u", "/"},
		{"", "/home/u", ""},
		{"/home/u", "", "u"},
	}
	for _, c := range cases {
		if got := dirLabel(c.cwd, c.home); got != c.want {
			t.Errorf("dirLabel(%q, %q) = %q, want %q", c.cwd, c.home, got, c.want)
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

func TestGitBranchReadsHeadUnderDotGit(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	writeFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/feat/labels\n")
	if got := gitBranch(repo); got != "feat/labels" {
		t.Fatalf("gitBranch at the root = %q, want feat/labels", got)
	}
	sub := filepath.Join(repo, "internal", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := gitBranch(sub); got != "feat/labels" {
		t.Fatalf("gitBranch in a subdirectory = %q, want feat/labels", got)
	}
}

func TestGitBranchIsEmptyOutsideACheckout(t *testing.T) {
	plain := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := gitBranch(plain); got != "" {
		t.Fatalf("gitBranch outside a checkout = %q, want empty", got)
	}
}

func TestGitBranchReadsAWorktreeCheckout(t *testing.T) {
	base := t.TempDir()
	main := filepath.Join(base, "main")
	writeFile(t, filepath.Join(main, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(main, ".git", "worktrees", "wt", "HEAD"), "ref: refs/heads/wt-branch\n")

	// The absolute form git writes.
	wt := filepath.Join(base, "wt")
	writeFile(t, filepath.Join(wt, ".git"), "gitdir: "+filepath.Join(main, ".git", "worktrees", "wt")+"\n")
	if got := gitBranch(wt); got != "wt-branch" {
		t.Fatalf("worktree branch = %q, want wt-branch", got)
	}

	// The relative form a submodule writes.
	sub := filepath.Join(base, "sub")
	writeFile(t, filepath.Join(sub, ".git"), "gitdir: ../main/.git/worktrees/wt\n")
	if got := gitBranch(sub); got != "wt-branch" {
		t.Fatalf("relative gitdir branch = %q, want wt-branch", got)
	}

	// A .git file that names no gitdir is not a checkout.
	odd := filepath.Join(base, "odd")
	writeFile(t, filepath.Join(odd, ".git"), "not a checkout\n")
	if got := gitBranch(odd); got != "" {
		t.Fatalf("malformed .git file gave %q, want empty", got)
	}
}

func TestGitBranchDetachedHeadIsTheShortHash(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	writeFile(t, filepath.Join(repo, ".git", "HEAD"), "0123456789abcdef0123456789abcdef01234567\n")
	if got := gitBranch(repo); got != "0123456" {
		t.Fatalf("detached HEAD = %q, want 0123456", got)
	}
	writeFile(t, filepath.Join(repo, ".git", "HEAD"), "garbage\n")
	if got := gitBranch(repo); got != "" {
		t.Fatalf("unreadable HEAD = %q, want empty", got)
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

func TestIsGeneratedSessionName(t *testing.T) {
	for name, want := range map[string]bool{
		"session-0":   true,
		"session-12":  true,
		"session-":    false,
		"session-x":   false,
		"mysession-1": false,
		"work":        false,
		"":            false,
	} {
		if got := IsGeneratedSessionName(name); got != want {
			t.Errorf("IsGeneratedSessionName(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestListingsDisagreeWhenAShellMoves: a cd moves no window, so without these
// two fields in the comparison a session that changed directory or branch kept
// its old label until something else bumped the cache generation.
func TestListingsDisagreeWhenAShellMoves(t *testing.T) {
	was := []SessionInfo{{Name: "session-0", Dir: "repo", Branch: "main"}}
	if listingsAgree(was, []SessionInfo{{Name: "session-0", Dir: "repo", Branch: "fix"}}) {
		t.Error("a branch change must not read as the same listing")
	}
	if listingsAgree(was, []SessionInfo{{Name: "session-0", Dir: "docs", Branch: "main"}}) {
		t.Error("a directory change must not read as the same listing")
	}
	if !listingsAgree(was, []SessionInfo{{Name: "session-0", Dir: "repo", Branch: "main"}}) {
		t.Error("an unchanged place must read as the same listing")
	}
}

// waitPlace polls the listing until the focused pane's place reads as wanted.
// The branch is read on its own goroutine, so a listing taken right after the
// cd can be one read behind.
func waitPlace(t *testing.T, sess *Session, dir, branch string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		info := sess.Info()
		if info.Dir == dir && info.Branch == branch {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("listing place = %q %q, want %q %q", info.Dir, info.Branch, dir, branch)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestASessionIsPlacedByItsFocusedPane runs the whole daemon-side path against
// a real shell: the spawn directory places the pane before it reports anything,
// an OSC 7 moves it, a directory outside a checkout has no branch, and a report
// from another machine is ignored.
func TestASessionIsPlacedByItsFocusedPane(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "labelrepo")
	writeFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/feature-x\n")
	plain := filepath.Join(base, "plaindir")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}

	sess := newTestSession(t)
	win, err := sess.AddDaemonWindowWith(NewWindowOptions{Title: "shell", Cwd: repo, Focus: true}, nil)
	if err != nil {
		t.Fatalf("AddDaemonWindowWith: %v", err)
	}
	waitPlace(t, sess, "labelrepo", "feature-x")

	p := sess.GetPTY(win.PTYID)
	if p == nil {
		t.Fatal("the window has no PTY")
	}
	feedVT(t, p, "\x1b]7;file://localhost"+plain+"\x07")
	waitPlace(t, sess, "plaindir", "")

	feedVT(t, p, "\x1b]7;file://another-machine/root\x07")
	time.Sleep(50 * time.Millisecond)
	if info := sess.Info(); info.Dir != "plaindir" {
		t.Fatalf("a remote report moved the pane to %q", info.Dir)
	}

	feedVT(t, p, "\x1b]7;file://localhost"+repo+"\x07")
	waitPlace(t, sess, "labelrepo", "feature-x")
	if !strings.HasSuffix(p.place.Cwd(), "labelrepo") {
		t.Fatalf("pty cwd = %q", p.place.Cwd())
	}
}
