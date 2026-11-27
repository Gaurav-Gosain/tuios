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

// TestSnapshotDropsAFileThatGrowsAsItIsRead grows a small untracked file to
// past the limit between stageAll's size check and git's read of it, as an
// agent that writes at that moment does. git stores it whole, so the snapshot
// reads the staged size back: the tree leaves the file out, the path is
// reported, and nothing reaches the blob, so git gc prunes it. A file that
// stays small is kept.
//
// Negative control: with dropLargeStaged not called, the tree holds the file
// and the path is not reported.
func TestSnapshotDropsAFileThatGrowsAsItIsRead(t *testing.T) {
	repo := testutil.GitRepo(t)
	ctx := context.Background()
	grows := filepath.Join(repo, "grows.bin")
	for name, body := range map[string]string{"grows.bin": "small\n", "small.txt": "small\n"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	big := strings.Repeat("x", 4096)
	stageAllBeforeAdd = func() {
		if err := os.WriteFile(grows, []byte(big), 0o644); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { stageAllBeforeAdd = nil })

	tree, late, err := snapshotTreeExcluding(ctx, repo, nil, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if len(late) != 1 || late[0] != "grows.bin" {
		t.Errorf("late = %v, want [grows.bin]", late)
	}
	files := testutil.Git(t, repo, "ls-tree", "-r", "--name-only", tree)
	if !strings.Contains(files, "small.txt") || strings.Contains(files, "grows.bin") {
		t.Errorf("the tree holds:\n%s\nwant small.txt and not grows.bin", files)
	}
	// git read the file whole, so its blob is stored, but no tree holds it.
	sha := testutil.Git(t, repo, "hash-object", grows)
	testutil.Git(t, repo, "gc", "-q", "--prune=now")
	if exec.Command("git", "-C", repo, "cat-file", "-e", sha).Run() == nil {
		t.Errorf("git gc kept the grown file's blob %s: something reaches it", sha[:7])
	}
}
