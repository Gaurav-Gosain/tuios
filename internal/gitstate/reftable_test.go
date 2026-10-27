package gitstate

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// withGitTracking swaps in a counting wrapper around the git a reftable read
// runs, and answers how many times it ran.
func withGitTracking(t *testing.T) *atomic.Int64 {
	t.Helper()
	Forget()
	var n atomic.Int64
	real := askGit
	askGit = func(root string, args ...string) (string, error) {
		n.Add(1)
		return real(root, args...)
	}
	t.Cleanup(func() { askGit = real; Forget() })
	return &n
}

// TestReftableReadsAskGitOnlyWhenARefMoves is the cost model for a repository
// whose refs are in reftable, where HEAD and every ref have to be asked of
// git. The rail reads the focused pane's repository every two seconds, and
// the daemon reads a session's on every save, so an answer that is not kept
// is a subprocess on every refresh of an idle client.
//
// The ways it could fail:
//   - The stamp moves on a read, because something git touches while it
//     only reads is part of it. Every read then asks again.
//   - The HEAD answer or the ref answer is asked for and never kept.
//   - Read and Branch keep their answers apart, so one asks for what the
//     other already knows.
//   - The answers are kept past a ref update. The second half catches it:
//     a commit moves the branch, and the next read must ask again and see
//     the new commit.
func TestReftableReadsAskGitOnlyWhenARefMoves(t *testing.T) {
	seed := testutil.GitRepo(t)
	ran := withGitTracking(t)
	repo := filepath.Join(t.TempDir(), "reft")
	if out, err := exec.Command("git", "clone", "-q", "--ref-format=reftable", seed, repo).CombinedOutput(); err != nil {
		t.Skipf("this git cannot make a reftable repository (git 2.45 or later can): %v: %s", err, out)
	}
	if !usesReftable(filepath.Join(repo, ".git")) {
		t.Fatal("the clone keeps its refs in files, so this would test nothing new")
	}

	// HEAD, then origin/main, which is main's upstream.
	const perRead = 2
	for range 3 {
		st, ok := Read(repo)
		if !ok || st.Branch != "main" || st.Upstream != "refs/remotes/origin/main" {
			t.Fatalf("Read = %+v, %v; want main following origin/main", st, ok)
		}
		if b := Branch(repo); b != "main" {
			t.Fatalf("Branch = %q, want main", b)
		}
	}
	if got := ran.Load(); got != perRead {
		t.Errorf("git ran %d times across three reads with no ref moving, want %d", got, perRead)
	}

	// The positive half: a ref update is seen at once.
	if err := os.WriteFile(filepath.Join(repo, "later.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo, "add", "later.txt")
	testutil.Git(t, repo, "commit", "-q", "-m", "later")
	want := testutil.Git(t, repo, "rev-parse", "HEAD")
	st, ok := Read(repo)
	if !ok || st.Ahead != 1 {
		t.Errorf("after a commit, Read = %+v, %v; want main one ahead of origin/main", st, ok)
	}
	if got := ran.Load(); got != 2*perRead {
		t.Errorf("git ran %d times after a commit moved main, want %d", got, 2*perRead)
	}
	if _, sha, _ := ReftableHead(filepath.Join(repo, ".git"), filepath.Join(repo, ".git"), repo); sha != want {
		t.Errorf("HEAD is %s after the commit, want %s", sha, want)
	}
}
