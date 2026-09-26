package testutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A unix socket address is a fixed-size field in the kernel, not a string:
// 104 bytes on Darwin, 108 on Linux, including the terminator. bind() answers
// a longer path with EINVAL, which surfaces as "bind: invalid argument" and
// names neither the path nor the length, so the failure reads like a broken
// socket rather than a long one.
const maxSocketPath = 104

// sockNameAllowance is what the caller still has to append to the directory
// this returns. The daemon puts its socket at <dir>/tuios/tuios.sock and its
// pid file at that plus ".pid", which is the longest of them.
const sockNameAllowance = len("/tuios/tuios.sock.pid")

// RuntimeDir returns a directory to use as XDG_RUNTIME_DIR in a test that
// starts a daemon, short enough that the socket path inside it still fits in a
// sockaddr_un.
//
// t.TempDir is not short enough on macOS. It roots at $TMPDIR, which is a
// per-user path under /var/folders about 49 characters long, and it appends
// the test's own name, so a long test name reaches 130 characters before the
// socket name is added. Every daemon test in
// the tree failed that way, and the error named the symptom rather than the
// cause.
//
// The directory is created under /tmp, which is short on both platforms that
// have unix sockets, and removed when the test ends.
func RuntimeDir(t testing.TB) string {
	t.Helper()

	base := "/tmp"
	if runtime.GOOS == "windows" {
		base = ""
	}
	dir, err := os.MkdirTemp(base, "tu")
	if err != nil {
		t.Fatalf("testutil: create runtime dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	// Fail here rather than at bind, where the kernel reports EINVAL and says
	// nothing about the length. Nothing in the tree should reach this, but a
	// machine with an unusual temp root would otherwise get the old mystery.
	if n := len(dir) + sockNameAllowance; n > maxSocketPath {
		t.Fatalf("testutil: runtime dir %s leaves a %d byte socket path, over the %d byte limit", dir, n, maxSocketPath)
	}
	return filepath.Clean(dir)
}
