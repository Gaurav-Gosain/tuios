package worktree

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sync/errgroup"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// benchWorktrees builds a repository with n worktrees, each carrying a
// modified file, an untracked file and an ignored tree the size of a small
// node_modules, which is the shape that makes a listing slow.
func benchWorktrees(b *testing.B, n int) []string {
	var t testing.T
	repo := testutil.GitRepo(&t)
	if out, err := run(repo, "commit", "--allow-empty", "-q", "-m", "init"); err != nil {
		b.Fatalf("commit: %v: %s", err, out)
	}
	paths := make([]string, 0, n)
	for i := range n {
		wt := filepath.Join(b.TempDir(), "wt"+string(rune('a'+i)))
		if out, err := run(repo, "worktree", "add", "-q", "-b", "feat/bench"+string(rune('a'+i)), "--", wt); err != nil {
			b.Fatalf("worktree add: %v: %s", err, out)
		}
		if err := os.WriteFile(filepath.Join(wt, ".gitignore"), []byte("cache/\n"), 0o644); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wt, "mod.txt"), []byte("changed"), 0o644); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wt, "untracked.txt"), []byte("x"), 0o644); err != nil {
			b.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(wt, "cache"), 0o755); err != nil {
			b.Fatal(err)
		}
		for j := range 300 {
			if err := os.WriteFile(filepath.Join(wt, "cache", "f"+string(rune('a'+j/26))+string(rune('a'+j%26))+".txt"), []byte("x"), 0o644); err != nil {
				b.Fatal(err)
			}
		}
		paths = append(paths, wt)
	}
	return paths
}

func benchOne(path string) {
	if _, _, err := ChangeCounts(path); err != nil {
		panic(err)
	}
	if _, err := OwnCommits(path); err != nil {
		panic(err)
	}
	if _, err := Ahead(path, "main"); err != nil {
		panic(err)
	}
	if _, err := Merged(path, path, "", "main"); err != nil {
		panic(err)
	}
}

func BenchmarkWorktreeRowSerial(b *testing.B) {
	paths := benchWorktrees(b, 8)
	b.ResetTimer()
	for b.Loop() {
		for _, p := range paths {
			benchOne(p)
		}
	}
}

func BenchmarkWorktreeRowParallel(b *testing.B) {
	paths := benchWorktrees(b, 8)
	b.ResetTimer()
	for b.Loop() {
		group, ctx := errgroup.WithContext(b.Context())
		group.SetLimit(8)
		for _, p := range paths {
			p := p
			group.Go(func() error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
				benchOne(p)
				return nil
			})
		}
		_ = group.Wait()
	}
}
