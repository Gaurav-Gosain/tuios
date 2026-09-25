package worktree

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// Every test here works on a repository testutil.GitRepo made under the test's
// temporary directory. Nothing touches any other repository.

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

func TestDetectDoesNotCallTheMainCheckoutAWorktree(t *testing.T) {
	repo := testutil.GitRepo(t)
	if info, ok := Detect(repo); ok {
		t.Errorf("Detect on the main checkout = %+v, want no worktree", info)
	}
	if info, ok := Detect(t.TempDir()); ok {
		t.Errorf("Detect on a plain directory = %+v, want no worktree", info)
	}
}
