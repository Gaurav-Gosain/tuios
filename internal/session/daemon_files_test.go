package session

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The rail's file section reads a directory on the machine the pane is on,
// which is the daemon's machine and not necessarily the client's. These cover
// the daemon's half: the listing, its order, its cap, and the sentence a failure
// turns into.

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

// touch makes an empty-ish file. Named apart from the package's existing
// writeFile helper, which takes contents.
func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestADirectoryListsFoldersFirstThenNames. The order is the daemon's because
// the cap is applied in whatever order the filesystem hands names back, so a
// client sorting a capped listing would be sorting an arbitrary subset of the
// directory and presenting it as the first of it.
func TestADirectoryListsFoldersFirstThenNames(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "beta.txt"))
	touch(t, filepath.Join(dir, "Alpha.txt"))
	mkdirAll(t, filepath.Join(dir, "zed"))
	mkdirAll(t, filepath.Join(dir, "Mid"))

	got := listDir(dir, 0)
	if got.Err != "" {
		t.Fatalf("listing failed: %s", got.Err)
	}
	var names []string
	for _, e := range got.Entries {
		names = append(names, e.Name)
	}
	want := []string{"Mid", "zed", "Alpha.txt", "beta.txt"}
	if len(names) != len(want) {
		t.Fatalf("listed %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("listed %v, want %v", names, want)
		}
	}
	// Case insensitively, or "Mid" sorts before "zed" only by accident of the
	// capital and "Alpha.txt" lands after "beta.txt".
	if got.Capped {
		t.Error("a four entry directory reported itself capped")
	}
}

// TestALongDirectorySaysItWasCut. A listing that silently stops is a listing
// that lies about what is in the folder.
func TestALongDirectorySaysItWasCut(t *testing.T) {
	dir := t.TempDir()
	for i := range 5 {
		touch(t, filepath.Join(dir, string(rune('a'+i))+".txt"))
	}

	got := listDir(dir, 3)
	if got.Err != "" {
		t.Fatalf("listing failed: %s", got.Err)
	}
	if len(got.Entries) != 3 {
		t.Errorf("a cap of 3 returned %d entries", len(got.Entries))
	}
	if !got.Capped {
		t.Error("a directory with more names than the cap did not say it was cut")
	}

	// The negative half: a cap the directory does not reach is not a cut.
	if full := listDir(dir, 50); full.Capped {
		t.Error("a directory smaller than the cap reported itself cut")
	}
}

// TestTheSameFolderSpelledTwoWaysIsOneFolder.
//
// The kernel hands back a path it has already resolved; a shell prints $PWD,
// which keeps whatever symlink the user walked in through. Comparing the two as
// strings calls every such pane a liar, and the spoof warning takes the file
// actions away with it, so the comparison has to be identity on disk.
//
// Negative control: reducing sameDirOnDisk to a string compare fails the
// symlink case here.
func TestTheSameFolderSpelledTwoWaysIsOneFolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege here")
	}
	root := t.TempDir()
	real := filepath.Join(root, "real")
	mkdirAll(t, real)
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot make a symlink: %v", err)
	}

	if !sameDirOnDisk(real, link) {
		t.Error("a folder reached through a symlink was read as a different folder")
	}
	if !sameDirOnDisk(real, real+"/.") {
		t.Error("a folder was not the same as itself spelled with a trailing dot")
	}

	other := filepath.Join(root, "other")
	mkdirAll(t, other)
	if sameDirOnDisk(real, other) {
		t.Error("two different folders were read as one")
	}
	if sameDirOnDisk(real, filepath.Join(root, "gone")) {
		t.Error("a folder that does not exist was read as the same as one that does")
	}
}

// TestAnEmptyPathIsNotAFolder guards the argument reaching this from a pane
// whose shell has not reported anywhere yet.
func TestAnEmptyPathIsNotAFolder(t *testing.T) {
	if got := listDir("", 0); got.Err == "" {
		t.Errorf("the empty path listed %d entries with no error", len(got.Entries))
	}
}
