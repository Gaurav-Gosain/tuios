package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestSnapshotLeavesOutAFileMadeAfterTheList makes a large untracked file
// after the caller listed the large ones, as an agent that writes while a
// turn start is taken does. The snapshot leaves it out, reports it, and git's
// object store never gets it.
//
// Negative control: with stageAll's own size check removed, the blob is in
// the object store and the path is not reported.
func TestSnapshotLeavesOutAFileMadeAfterTheList(t *testing.T) {
	repo := testutil.GitRepo(t)
	ctx := context.Background()
	listed, err := largeUntracked(ctx, repo, 1024)
	if err != nil || len(listed) != 0 {
		t.Fatalf("largeUntracked = %v, %v; want none", listed, err)
	}
	large := filepath.Join(repo, "large.bin")
	if err := os.WriteFile(large, []byte(strings.Repeat("x", 4096)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "small.txt"), []byte("small\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree, late, err := snapshotTreeExcluding(ctx, repo, listed, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if len(late) != 1 || late[0] != "large.bin" {
		t.Errorf("late = %v, want [large.bin]", late)
	}
	files := testutil.Git(t, repo, "ls-tree", "-r", "--name-only", tree)
	if !strings.Contains(files, "small.txt") || strings.Contains(files, "large.bin") {
		t.Errorf("the tree holds:\n%s\nwant small.txt and not large.bin", files)
	}
	sha := testutil.Git(t, repo, "hash-object", large)
	if exec.Command("git", "-C", repo, "cat-file", "-e", sha).Run() == nil {
		t.Errorf("the object store holds the large file's blob %s", sha[:7])
	}
}
