package review

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}

// indexHash is the hash of the repository's index file, so a test can tell
// the index was not written.
func indexHash(t *testing.T, repo string) string {
	t.Helper()
	path := testutil.Git(t, repo, "rev-parse", "--git-path", "index")
	if !filepath.IsAbs(path) {
		path = filepath.Join(repo, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func fileOf(t *testing.T, d *Diff, path string) *File {
	t.Helper()
	f := d.FileByPath(path)
	if f == nil {
		var names []string
		for _, x := range d.Files {
			names = append(names, x.Status+" "+x.Path)
		}
		t.Fatalf("%s is not in the diff: %v", path, names)
	}
	return f
}

// TestBuildLeavesTheIndexAndWorkingTreeAlone: a diff of a worktree with a
// staged change, an unstaged change, an untracked file and an ignored file
// shows the first three, and afterwards git status, the index file and every
// file are exactly as they were.
func TestBuildLeavesTheIndexAndWorkingTreeAlone(t *testing.T) {
	repo := testutil.GitRepo(t)
	write(t, repo, ".gitignore", "*.log\n")
	write(t, repo, "keep.go", "package keep\n")
	testutil.Git(t, repo, "add", ".")
	testutil.Git(t, repo, "commit", "-q", "-m", "base")

	write(t, repo, "keep.go", "package keep\n\nvar Staged = 1\n")
	testutil.Git(t, repo, "add", "keep.go")
	write(t, repo, "README", "hello\nunstaged\n")
	write(t, repo, "dir/new.txt", "a\nb\n")
	write(t, repo, "debug.log", "ignored\n")

	status := testutil.Git(t, repo, "status", "--porcelain", "--untracked-files=all")
	index := indexHash(t, repo)

	base, err := ResolveBase(ctx(t), repo, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	d, err := Build(ctx(t), Options{Dir: repo, Base: base, Context: 3})
	if err != nil {
		t.Fatal(err)
	}
	if got := testutil.Git(t, repo, "status", "--porcelain", "--untracked-files=all"); got != status {
		t.Errorf("git status changed:\nbefore\n%s\nafter\n%s", status, got)
	}
	if got := indexHash(t, repo); got != index {
		t.Error("the index file changed")
	}
	if f := fileOf(t, d, "keep.go"); f.Status != StatusModified || f.Added != 2 {
		t.Errorf("staged file = %+v", f)
	}
	if f := fileOf(t, d, "README"); f.Status != StatusModified || f.Added != 1 || f.Removed != 0 {
		t.Errorf("unstaged file = %+v", f)
	}
	if f := fileOf(t, d, "dir/new.txt"); f.Status != StatusUntracked || f.Added != 2 {
		t.Errorf("untracked file = %+v", f)
	}
	if d.FileByPath("debug.log") != nil {
		t.Error("an ignored file is in the diff")
	}
	if d.Totals.Files != 3 || d.Totals.Added != 5 {
		t.Errorf("totals = %+v", d.Totals)
	}
	if !d.Uncommitted || d.Base != "HEAD" || d.TreeSHA == "" {
		t.Errorf("diff header = base %q uncommitted %v tree %q", d.Base, d.Uncommitted, d.TreeSHA)
	}
	readme := fileOf(t, d, "README")
	if len(readme.Hunks) != 1 {
		t.Fatalf("README hunks = %+v", readme.Hunks)
	}
	lines := readme.Hunks[0].Lines
	last := lines[len(lines)-1]
	if last.Op != OpAdd || last.Text != "unstaged" || last.New != 2 {
		t.Errorf("README's last line = %+v", last)
	}
}

func TestResolveBase(t *testing.T) {
	repo := testutil.GitRepo(t)
	head := testutil.Git(t, repo, "rev-parse", "HEAD")

	var refErr *RefError
	if _, err := ResolveBase(ctx(t), repo, "no-such-branch", "", false); !errors.As(err, &refErr) {
		t.Errorf("a named base that does not resolve = %v, want a RefError", err)
	}
	if _, err := ResolveBase(ctx(t), repo, "--output=/tmp/x", "", false); !errors.As(err, &refErr) {
		t.Errorf("a base that reads as an option = %v, want a RefError", err)
	}

	// A recorded base that is gone falls through to HEAD.
	b, err := ResolveBase(ctx(t), repo, "", "deleted-branch", false)
	if err != nil || b.Name != "HEAD" || !b.Uncommitted || b.SHA != head || b.From != "head" {
		t.Errorf("gone recorded base = %+v (%v)", b, err)
	}
	b, err = ResolveBase(ctx(t), repo, "", "main", false)
	if err != nil || b.Name != "main" || b.From != "worktree" || b.SHA != head {
		t.Errorf("recorded base = %+v (%v)", b, err)
	}

	// With an upstream, its merge base.
	testutil.Git(t, repo, "branch", "upstream-main")
	testutil.Git(t, repo, "checkout", "-q", "-b", "topic")
	testutil.Git(t, repo, "branch", "--set-upstream-to=upstream-main")
	write(t, repo, "t.txt", "t\n")
	testutil.Git(t, repo, "add", ".")
	testutil.Git(t, repo, "commit", "-q", "-m", "topic")
	b, err = ResolveBase(ctx(t), repo, "", "", false)
	if err != nil || b.Name != "upstream-main" || b.From != "upstream" || b.SHA != head {
		t.Errorf("upstream base = %+v (%v)", b, err)
	}

	// A repository with no commit diffs from the empty tree.
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, empty, "init", "-q")
	write(t, empty, "first.txt", "one\n")
	b, err = ResolveBase(ctx(t), empty, "", "", false)
	if err != nil || !b.Uncommitted || b.SHA == "" {
		t.Fatalf("empty repository base = %+v (%v)", b, err)
	}
	d, err := Build(ctx(t), Options{Dir: empty, Base: b})
	if err != nil {
		t.Fatal(err)
	}
	if f := fileOf(t, d, "first.txt"); f.Status != StatusUntracked || f.Added != 1 {
		t.Errorf("first file = %+v", f)
	}
}

func TestFileLinesStaysInsideTheRoot(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.txt", "one\r\ntwo\n")
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.txt")); err != nil {
		t.Skip("no symlinks here")
	}
	lines, err := FileLines(root, "a.txt")
	if err != nil || len(lines) != 2 || lines[0] != "one" || lines[1] != "two" {
		t.Errorf("a.txt = %q (%v)", lines, err)
	}
	for _, p := range []string{"link.txt", "../x", "/etc/passwd", "a/../../x", ""} {
		if _, err := FileLines(root, p); err == nil {
			t.Errorf("FileLines(%q) read a file outside the root", p)
		}
	}
}
