//go:build unix

package app

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"tuios.dev/vt"
)

// TestKeptFileIsCheckedAgain is a security boundary. A t=f image is kept as a
// path and read when a screenshot asks, which can be long after
// kittyMediumPath checked it. The pane owns the file and can put something
// else in its place in between. The ways that could go wrong:
//
//  1. A symlink in its place leads the read to a file the check would refuse,
//     such as one under /proc, and its bytes land in the picture as pixels.
//  2. A symlink in place of a folder on the path does the same.
//  3. A named pipe in its place blocks the open until a writer comes.
//
// Each must be refused, and neither may block.
func TestKeptFileIsCheckedAgain(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pixels := make([]byte, 2*2*3)
	elsewhere := filepath.Join(dir, "elsewhere")
	if err := os.Mkdir(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(elsewhere, "frame")
	if err := os.WriteFile(other, pixels, 0o600); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(dir, "folder")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(folder, "frame")
	k := keptImage{format: vt.KittyFormatRGB, width: 2, height: 2, path: path}
	write := func() {
		_ = os.Remove(path)
		if err := os.WriteFile(path, pixels, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write()
	if _, err := k.decode(shotImageMaxPixels); err != nil {
		t.Fatalf("the file as checked does not decode: %v", err)
	}

	write()
	_ = os.Remove(path)
	if err := os.Symlink(other, path); err != nil {
		t.Fatal(err)
	}
	if _, err := k.decode(shotImageMaxPixels); err == nil {
		t.Fatal("a symlink put in place of the file was followed")
	}

	_ = os.Remove(path)
	if err := os.Rename(folder, folder+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, folder); err != nil {
		t.Fatal(err)
	}
	if _, err := k.decode(shotImageMaxPixels); err == nil {
		t.Fatal("a symlink put in place of a folder on the path was followed")
	}
	_ = os.Remove(folder)
	if err := os.Rename(folder+".old", folder); err != nil {
		t.Fatal(err)
	}

	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := k.decode(shotImageMaxPixels)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a named pipe put in place of the file was read")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a named pipe put in place of the file blocked the read")
	}
}
