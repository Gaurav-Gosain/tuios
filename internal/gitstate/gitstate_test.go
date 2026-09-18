package gitstate

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// withTracking swaps in a counting wrapper around the one subprocess this
// package runs, and answers how many times it ran.
func withTracking(t *testing.T) *atomic.Int64 {
	t.Helper()
	Forget()
	var n atomic.Int64
	real := countCommits
	countCommits = func(root, head, upstream string) (int, int, error) {
		n.Add(1)
		return real(root, head, upstream)
	}
	t.Cleanup(func() { countCommits = real; Forget() })
	return &n
}

// tracked repo, with an origin it is set to follow.
func repoWithUpstream(t *testing.T) string {
	t.Helper()
	repo := testutil.GitRepo(t)
	bare := filepath.Join(filepath.Dir(repo), "origin.git")
	testutil.Git(t, repo, "init", "--bare", bare)
	testutil.Git(t, repo, "remote", "add", "origin", bare)
	testutil.Git(t, repo, "push", "-u", "origin", "main")
	return repo
}

// TestAPlainCheckoutReportsItsBranch. Detect in the worktree package answers
// false for a plain checkout on purpose, because it is asking "is this a linked
// worktree". This package is asking a different question and has to answer for
// the ordinary case, which is most of them.
func TestAPlainCheckoutReportsItsBranch(t *testing.T) {
	withTracking(t)
	repo := testutil.GitRepo(t)

	st, ok := Read(repo)
	if !ok {
		t.Fatal("a plain checkout is not reported as a repository at all")
	}
	if st.Branch != "main" {
		t.Errorf("branch is %q, want main", st.Branch)
	}
	if st.Repo != filepath.Base(repo) {
		t.Errorf("repo is %q, want %q", st.Repo, filepath.Base(repo))
	}
	if st.Root != repo {
		t.Errorf("root is %q, want %q", st.Root, repo)
	}
}

// TestABranchWithNoUpstreamHasNoCounts. Zero and zero is what a branch level
// with its upstream reports, so a branch that follows nothing must be
// distinguishable from one that is in step. Otherwise a row prints "in step"
// for a branch that has nowhere to be in step with.
func TestABranchWithNoUpstreamHasNoCounts(t *testing.T) {
	ran := withTracking(t)
	repo := testutil.GitRepo(t)

	st, ok := Read(repo)
	if !ok {
		t.Fatal("not a repository")
	}
	if st.HasUpstream() {
		t.Errorf("a branch with no remote reports upstream %q", st.Upstream)
	}
	if ran.Load() != 0 {
		t.Errorf("counting ran %d times for a branch with nothing to count against", ran.Load())
	}
}

// TestAheadAndBehindAreCounted, and the directions are not swapped. The
// left-right order of git's own output is the thing most easily got backwards,
// so both are asserted in one repository rather than separately.
func TestAheadAndBehindAreCounted(t *testing.T) {
	withTracking(t)
	repo := repoWithUpstream(t)

	if err := os.WriteFile(filepath.Join(repo, "ahead.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo, "add", "-A")
	testutil.Git(t, repo, "commit", "-m", "local work")

	st, ok := Read(repo)
	if !ok {
		t.Fatal("not a repository")
	}
	if st.Ahead != 1 || st.Behind != 0 {
		t.Errorf("after one local commit: ahead %d behind %d, want 1 and 0", st.Ahead, st.Behind)
	}

	// Now move the upstream on without moving HEAD, which is what a fetch does.
	// Rewinding the remote-tracking ref by one is the same shape and needs no
	// second clone.
	testutil.Git(t, repo, "update-ref", "refs/remotes/origin/main", "HEAD~1")
	Forget()
	st, ok = Read(repo)
	if !ok {
		t.Fatal("not a repository")
	}
	if st.Ahead != 1 {
		t.Errorf("ahead is %d, want 1", st.Ahead)
	}
}

// TestTheCountsAreNotRecomputedWhileNothingMoves is the package's whole reason
// to exist. A sidebar refreshes on a timer, and a subprocess per refresh per
// pane is what makes a sidebar stutter on a large repository.
//
// Negative control: with the hash comparison in Read removed, the second and
// third reads each spend a subprocess and this fails with 3.
func TestTheCountsAreNotRecomputedWhileNothingMoves(t *testing.T) {
	ran := withTracking(t)
	repo := repoWithUpstream(t)

	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo, "add", "-A")
	testutil.Git(t, repo, "commit", "-m", "work")

	for range 3 {
		if _, ok := Read(repo); !ok {
			t.Fatal("not a repository")
		}
	}
	if got := ran.Load(); got != 1 {
		t.Errorf("counting ran %d times across three reads with nothing moving, want 1", got)
	}

	// And it does run again once HEAD moves, or the cache would be a bug that
	// happens to make the test above pass.
	testutil.Git(t, repo, "commit", "--allow-empty", "-m", "more")
	if _, ok := Read(repo); !ok {
		t.Fatal("not a repository")
	}
	if got := ran.Load(); got != 2 {
		t.Errorf("counting ran %d times after HEAD moved, want 2", got)
	}
}

// TestALinkedWorktreeReportsItsOwnBranch. HEAD is per worktree and the
// remote-tracking refs are shared, so reading the branch out of the shared git
// directory reports the main checkout's branch on every worktree of the
// repository. That is the case worktree sessions are for, so it is the case
// that must not be wrong.
func TestALinkedWorktreeReportsItsOwnBranch(t *testing.T) {
	withTracking(t)
	repo := testutil.GitRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "wt-feature")
	testutil.Git(t, repo, "worktree", "add", "-b", "feature", wt)

	st, ok := Read(wt)
	if !ok {
		t.Fatal("a linked worktree is not reported as a repository")
	}
	if st.Branch != "feature" {
		t.Errorf("the worktree reports branch %q, want feature", st.Branch)
	}
	// It is the same repository, so it carries the same name. That is what lets
	// the rail group a repository's worktrees under one heading.
	if st.Repo != filepath.Base(repo) {
		t.Errorf("the worktree reports repo %q, want %q", st.Repo, filepath.Base(repo))
	}

	main, ok := Read(repo)
	if !ok {
		t.Fatal("the main checkout is not reported as a repository")
	}
	if main.Branch == st.Branch {
		t.Errorf("the main checkout and its worktree both report %q, so HEAD is being read from the shared directory", st.Branch)
	}
}

// TestADetachedHeadStillHasAName: a row has to print something, and the short
// hash is the only name a detached HEAD has.
func TestADetachedHeadStillHasAName(t *testing.T) {
	withTracking(t)
	repo := testutil.GitRepo(t)
	testutil.Git(t, repo, "checkout", "--detach")

	st, ok := Read(repo)
	if !ok {
		t.Fatal("a detached checkout is not reported as a repository")
	}
	if !st.Detached {
		t.Error("a detached HEAD is not reported as detached")
	}
	if len(st.Branch) != 7 {
		t.Errorf("the detached name is %q, want a seven character hash", st.Branch)
	}
	if st.HasUpstream() {
		t.Errorf("a detached HEAD reports upstream %q", st.Upstream)
	}
}

// TestPackedRefsAreResolved. A repository that has been fetched a few times
// keeps its remote-tracking refs in packed-refs rather than as loose files, so
// a reader that only looks for loose files reports no upstream on exactly the
// repositories that have one.
func TestPackedRefsAreResolved(t *testing.T) {
	withTracking(t)
	repo := repoWithUpstream(t)
	testutil.Git(t, repo, "pack-refs", "--all")

	loose := filepath.Join(repo, ".git", "refs", "remotes", "origin", "main")
	if _, err := os.Stat(loose); err == nil {
		t.Skip("this git kept the loose ref, so there is nothing to resolve from packed-refs")
	}

	st, ok := Read(repo)
	if !ok {
		t.Fatal("not a repository")
	}
	if !st.HasUpstream() {
		t.Error("no upstream found once the refs were packed")
	}
}

// TestADirectoryOutsideARepositoryIsNotOne, and is remembered as not being one.
func TestADirectoryOutsideARepositoryIsNotOne(t *testing.T) {
	withTracking(t)
	dir := t.TempDir()
	if _, ok := Read(dir); ok {
		t.Fatalf("%s is reported as a repository", dir)
	}

	mu.Lock()
	_, remembered := misses[filepath.Clean(dir)]
	mu.Unlock()
	if !remembered {
		// The walk goes to the filesystem root, so not remembering it means
		// every refresh repeats that walk for every pane in a home directory.
		t.Error("a directory that is not in a repository was not remembered as such")
	}
}

// TestAnEmptyDirectoryIsNotARepository guards the argument that reaches this
// from a pane whose shell has not reported anywhere yet.
func TestAnEmptyDirectoryIsNotARepository(t *testing.T) {
	withTracking(t)
	if _, ok := Read(""); ok {
		t.Error("the empty path is reported as a repository")
	}
}

// TestUpstreamOfReadsTheConfig covers the shapes the config can take without
// needing a remote for each: a plain remote branch, and a branch tracking
// another branch in the same repository, which git spells with a remote of ".".
func TestUpstreamOfReadsTheConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := strings.Join([]string{
		`[core]`,
		`	bare = false`,
		`[branch "main"]`,
		`	remote = origin`,
		`	merge = refs/heads/main`,
		`[branch "local"]`,
		`	remote = .`,
		`	merge = refs/heads/main`,
		`[branch "orphan"]`,
		`	merge = refs/heads/main`,
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := upstreamOf(dir, "main"); got != "refs/remotes/origin/main" {
		t.Errorf("main tracks %q, want refs/remotes/origin/main", got)
	}
	if got := upstreamOf(dir, "local"); got != "refs/heads/main" {
		t.Errorf("a branch tracking a local branch resolves to %q, want refs/heads/main", got)
	}
	if got := upstreamOf(dir, "orphan"); got != "" {
		t.Errorf("a branch with a merge and no remote resolves to %q, want nothing", got)
	}
	if got := upstreamOf(dir, "absent"); got != "" {
		t.Errorf("a branch with no section resolves to %q, want nothing", got)
	}
}
