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
