//go:build unix

package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The cross-device case needs a second filesystem at a known place and the
// device number from a unix stat, so it runs on unix only.

// TestTrashOnAnotherDiskNamesTheWayRound is the cross-filesystem gap, and it is
// the one thing the home trash cannot do. It needs two real filesystems, so it
// skips where there is only one.
func TestTrashOnAnotherDiskNamesTheWayRound(t *testing.T) {
	trash := tempTrash(t)
	if err := os.MkdirAll(filepath.Join(trash, "files"), 0o700); err != nil {
		t.Fatal(err)
	}

	other, err := os.MkdirTemp("/dev/shm", "tuios-trash-")
	if err != nil {
		t.Skipf("no second filesystem to test against: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(other) })
	if sameDevice(t, trash, other) {
		t.Skip("the trash and /dev/shm are on one filesystem here")
	}

	path := filepath.Join(other, "elsewhere.txt")
	mustWrite(t, path, "body")
	done, err := trashPaths([]string{path}, time.Now())
	if done != 0 || err == nil {
		t.Fatalf("trashing across filesystems returned %d, %v; it cannot work", done, err)
	}
	if !errors.Is(err, syscall.EXDEV) {
		t.Fatalf("the error was %v, want EXDEV", err)
	}
	got := trashError(err)
	if !strings.Contains(got, "another disk") || !strings.Contains(got, "permanent delete") {
		t.Errorf("the message is %q; it must name the disk and the way round it", got)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Errorf("the file was lost by a trash that could not work: %v", err)
	}
}

// sameDevice reports whether two paths sit on one filesystem.
func sameDevice(t *testing.T, a, b string) bool {
	t.Helper()
	sa, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := os.Stat(b)
	if err != nil {
		t.Fatal(err)
	}
	ra, ok1 := sa.Sys().(*syscall.Stat_t)
	rb, ok2 := sb.Sys().(*syscall.Stat_t)
	if !ok1 || !ok2 {
		t.Skip("this system does not report device numbers")
	}
	return ra.Dev == rb.Dev
}
