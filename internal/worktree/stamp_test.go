package worktree

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestStampSeesWhatChanged: a stamp says no change on the tree it was made
// from, and a change for an edited file, a removed one and a new one, but not
// for a large untracked file the snapshot left out.
func TestStampSeesWhatChanged(t *testing.T) {
	dir := testutil.GitRepo(t)
	ctx := context.Background()
	big := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(big, make([]byte, 2<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	st, skipped, err := SnapshotStamp(ctx, dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if len(skipped) != 1 {
		t.Fatalf("the snapshot left out %v, want big.bin", skipped)
	}
	changed := func(why string, want bool) {
		t.Helper()
		got, err := st.Changed(ctx, dir)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s: Changed = %v, want %v", why, got, want)
		}
	}
	changed("nothing changed", false)
	readme := filepath.Join(dir, "README")
	if err := os.WriteFile(readme, []byte("hello, changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed("README edited", true)
	if err := os.WriteFile(readme, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed("a new file", true)
	if err := os.Remove(filepath.Join(dir, "new.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(readme); err != nil {
		t.Fatal(err)
	}
	changed("README removed", true)
}
