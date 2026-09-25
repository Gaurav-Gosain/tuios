package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// Every test here works on a repository testutil.GitRepo made under the test's
// temporary directory. Nothing touches any other repository.

func TestDetectNamesALinkedWorktreeByRepoAndBranch(t *testing.T) {
	repo := testutil.GitRepo(t)
	path := filepath.Join(t.TempDir(), "wt")
	if _, err := Add(repo, path, "feat/retry", ""); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// A directory under the worktree resolves to the worktree, not to itself.
	sub := filepath.Join(path, "deep", "er")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	info, ok := Detect(sub)
	if !ok {
		t.Fatalf("Detect(%s) found no worktree", sub)
	}
	if info.Repo != "repo" || info.Branch != "feat/retry" {
		t.Errorf("Detect = %+v, want repo %q on branch %q", info, "repo", "feat/retry")
	}
	if info.Path != path {
		t.Errorf("Detect path = %q, want the worktree root %q", info.Path, path)
	}
	if info.RepoRoot != repo {
		t.Errorf("Detect repo root = %q, want %q", info.RepoRoot, repo)
	}
}

func TestDetectDoesNotCallTheMainCheckoutAWorktree(t *testing.T) {
	repo := testutil.GitRepo(t)
	if info, ok := Detect(repo); ok {
		t.Errorf("Detect on the main checkout = %+v, want no worktree", info)
	}
	if info, ok := Detect(t.TempDir()); ok {
		t.Errorf("Detect on a plain directory = %+v, want no worktree", info)
	}
}

// TestDetectReadsTheGitFilesByHand lays out the files git writes for a linked
// worktree without running git, so each part Detect depends on can be taken
// away on its own: a relative gitdir, a gitdir under modules/ as a submodule
// has, and a missing commondir.
func TestDetectReadsTheGitFilesByHand(t *testing.T) {
	write := func(t *testing.T, path, data string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// layout makes <tmp>/main/.git/<kind>/wt as the gitdir and <tmp>/wt as the
	// tree, with the tree's .git file naming the gitdir by a relative path.
	layout := func(t *testing.T, kind string, commondir bool, head string) (tree, main string) {
		root := t.TempDir()
		main = filepath.Join(root, "main")
		gitdir := filepath.Join(main, ".git", kind, "wt")
		tree = filepath.Join(root, "wt")
		write(t, filepath.Join(tree, ".git"), "gitdir: ../main/.git/"+kind+"/wt\n")
		write(t, filepath.Join(gitdir, "HEAD"), head+"\n")
		if commondir {
			write(t, filepath.Join(gitdir, "commondir"), "../..\n")
		}
		return tree, main
	}

	t.Run("linked worktree", func(t *testing.T) {
		tree, main := layout(t, "worktrees", true, "ref: refs/heads/feat/x")
		info, ok := Detect(tree)
		want := Info{Repo: "main", RepoRoot: main, Branch: "feat/x", Path: tree}
		if !ok || info != want {
			t.Errorf("Detect = %+v, %v, want %+v", info, ok, want)
		}
	})
	t.Run("submodule", func(t *testing.T) {
		tree, _ := layout(t, "modules", true, "ref: refs/heads/main")
		if info, ok := Detect(tree); ok {
			t.Errorf("Detect on a submodule = %+v, want no worktree", info)
		}
	})
	t.Run("no commondir", func(t *testing.T) {
		tree, _ := layout(t, "worktrees", false, "ref: refs/heads/main")
		if info, ok := Detect(tree); ok {
			t.Errorf("Detect without commondir = %+v, want no worktree", info)
		}
	})
}

func TestDetectReadsADetachedHead(t *testing.T) {
	repo := testutil.GitRepo(t)
	path := filepath.Join(t.TempDir(), "wt")
	testutil.Git(t, repo, "worktree", "add", "--detach", path)
	info, ok := Detect(path)
	if !ok {
		t.Fatal("Detect found no worktree")
	}
	if !strings.HasPrefix(info.Branch, "detached@") {
		t.Errorf("branch = %q, want detached@<hash>", info.Branch)
	}
}

func TestRootFindsTheMainCheckoutFromAWorktree(t *testing.T) {
	repo := testutil.GitRepo(t)
	path := filepath.Join(t.TempDir(), "wt")
	if _, err := Add(repo, path, "x", ""); err != nil {
		t.Fatalf("Add: %v", err)
	}
	for _, dir := range []string{repo, path, filepath.Join(path)} {
		root, err := Root(dir)
		if err != nil {
			t.Fatalf("Root(%s): %v", dir, err)
		}
		if root != repo {
			t.Errorf("Root(%s) = %q, want %q", dir, root, repo)
		}
	}
	if _, err := Root(t.TempDir()); err == nil {
		t.Error("Root on a plain directory returned no error")
	}
}

func TestAddCreatesTheBranchOnlyWhenItIsMissing(t *testing.T) {
	repo := testutil.GitRepo(t)
	testutil.Git(t, repo, "branch", "existing")
	base := t.TempDir()

	created, err := Add(repo, filepath.Join(base, "a"), "existing", "")
	if err != nil {
		t.Fatalf("Add existing: %v", err)
	}
	if created {
		t.Error("Add reported it created a branch that already existed")
	}
	created, err = Add(repo, filepath.Join(base, "b"), "fresh", "main")
	if err != nil {
		t.Fatalf("Add fresh: %v", err)
	}
	if !created {
		t.Error("Add did not report creating a new branch")
	}
	if !BranchExists(repo, "fresh") {
		t.Error("the fresh branch does not exist after Add")
	}
	if _, err := Add(repo, filepath.Join(base, "b"), "other", ""); err == nil {
		t.Error("Add reused a path that already existed")
	}
}

func TestChangesCountsUntrackedAndModifiedFiles(t *testing.T) {
	repo := testutil.GitRepo(t)
	path := filepath.Join(t.TempDir(), "wt")
	if _, err := Add(repo, path, "x", ""); err != nil {
		t.Fatalf("Add: %v", err)
	}
	n, err := Changes(path)
	if err != nil || n != 0 {
		t.Fatalf("Changes on a clean worktree = %d, %v; want 0", n, err)
	}
	if err := os.WriteFile(filepath.Join(path, "README"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err = Changes(path)
	if err != nil || n != 2 {
		t.Fatalf("Changes = %d, %v; want 2", n, err)
	}
}

func TestDiffAndAheadDescribeTheWork(t *testing.T) {
	repo := testutil.GitRepo(t)
	path := filepath.Join(t.TempDir(), "wt")
	if _, err := Add(repo, path, "x", "main"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "README"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, path, "commit", "-q", "-am", "change")
	if err := os.WriteFile(filepath.Join(path, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ahead, err := Ahead(path, "main")
	if err != nil || ahead != 1 {
		t.Errorf("Ahead = %d, %v; want 1", ahead, err)
	}
	diff, err := Diff(path, false)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(diff, "untracked:\n  new.txt") {
		t.Errorf("Diff does not list the untracked file:\n%s", diff)
	}
	log, err := Log(path, "main")
	if err != nil || !strings.Contains(log, "change") {
		t.Errorf("Log = %q, %v; want the commit", log, err)
	}
}

func TestSlugAndSessionName(t *testing.T) {
	cases := map[string]string{
		"feat/retry":     "feat-retry",
		"fix//double":    "fix-double",
		"release-1.2":    "release-1.2",
		"/leading/":      "leading",
		"weird name!":    "weird-name",
		"":               "branch",
		"detached@abc12": "detached-abc12",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
	if got := SessionName("tuios", "feat/retry"); got != "tuios-feat-retry" {
		t.Errorf("SessionName = %q", got)
	}
	if got := PathFor("/data", "/src/tuios", "feat/retry"); got != "/data/tuios/feat-retry" {
		t.Errorf("PathFor = %q", got)
	}
}

func TestValidBranch(t *testing.T) {
	if err := ValidBranch("feat/ok"); err != nil {
		t.Errorf("a valid name was refused: %v", err)
	}
	for _, bad := range []string{"", "  ", "bad..name", "-lead", "a b", "end/"} {
		if err := ValidBranch(bad); err == nil {
			t.Errorf("ValidBranch(%q) accepted it", bad)
		}
	}
}

func TestCurrentBranchReadsHEADNow(t *testing.T) {
	repo := testutil.GitRepo(t)
	path := filepath.Join(t.TempDir(), "wt")
	if _, err := Add(repo, path, "feat/one", ""); err != nil {
		t.Fatal(err)
	}
	if b, err := CurrentBranch(path); err != nil || b != "feat/one" {
		t.Errorf("CurrentBranch = %q, %v; want feat/one", b, err)
	}
	testutil.Git(t, path, "checkout", "-q", "-b", "heads/two")
	if b, err := CurrentBranch(path); err != nil || b != "heads/two" {
		t.Errorf("after a switch CurrentBranch = %q, %v; want heads/two", b, err)
	}
	testutil.Git(t, path, "checkout", "-q", "--detach")
	if b, err := CurrentBranch(path); err != nil || b != "" {
		t.Errorf("detached CurrentBranch = %q, %v; want empty and no error", b, err)
	}
	if _, err := CurrentBranch(t.TempDir()); err == nil {
		t.Error("CurrentBranch outside a repository reported no error")
	}
}

func TestDeleteBranchRemovesABranch(t *testing.T) {
	repo := testutil.GitRepo(t)
	testutil.Git(t, repo, "branch", "gone")
	if err := DeleteBranch(repo, "gone"); err != nil || BranchExists(repo, "gone") {
		t.Errorf("DeleteBranch: %v, exists %v", err, BranchExists(repo, "gone"))
	}
	if err := DeleteBranch(repo, "-D"); err == nil {
		t.Error("DeleteBranch accepted a name that reads as an option")
	}
}
