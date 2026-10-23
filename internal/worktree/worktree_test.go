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

func TestParseWorktreeList(t *testing.T) {
	out := "worktree /src/app\nHEAD 1111111111111111111111111111111111111111\nbranch refs/heads/main\n\n" +
		"worktree /wt/app/feat-x\nHEAD 2222222222222222222222222222222222222222\nbranch refs/heads/feat/x\n\n" +
		"worktree /wt/app/old\nHEAD 3333333333333333333333333333333333333333\ndetached\nprunable gitdir file points to non-existent location\n\n"
	got := parseWorktreeList(out)
	want := []Entry{
		{Path: "/src/app", Branch: "main"},
		{Path: "/wt/app/feat-x", Branch: "feat/x", Linked: true},
		{Path: "/wt/app/old", Detached: true, Prunable: true, Linked: true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestIgnoredReadsAPathWithASpace pins the -z read of git status: without it
// git quotes a path with a space or a non-ASCII character, and the quoted form
// lstat's as nothing, failing the walk a clean worktree never fails.
func TestIgnoredReadsAPathWithASpace(t *testing.T) {
	repo := testutil.GitRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	out, err := run(repo, "worktree", "add", "-b", "feat/spaces", "--", wt)
	if err != nil {
		t.Fatalf("worktree add: %v: %s", err, out)
	}
	for _, name := range []string{"notes draft.txt", "résumé.txt"} {
		if err := os.WriteFile(filepath.Join(wt, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	n, err := Ignored(wt)
	if err != nil {
		t.Fatalf("Ignored on untracked names git would quote: %v", err)
	}
	if n != 0 {
		t.Errorf("Ignored counted %d files, want 0: nothing is ignored", n)
	}

	if _, err := NestedRepo(wt); err != nil {
		t.Fatalf("NestedRepo on untracked names git would quote: %v", err)
	}

	ignored := filepath.Join(wt, "cache dir")
	if err := os.MkdirAll(ignored, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ignored, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitignore := filepath.Join(wt, ".gitignore")
	if err := os.WriteFile(gitignore, []byte("cache dir/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	n, err = Ignored(wt)
	if err != nil {
		t.Fatalf("Ignored on an ignored directory with a space: %v", err)
	}
	if n != 1 {
		t.Errorf("Ignored counted %d files, want the 1 in \"cache dir\"", n)
	}
}
