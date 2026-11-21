package testutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/serverscope"
)

const maxSocketPath = 104

const sockNameAllowance = len("/tuios/tuios.sock.pid") + serverscope.SocketPathGrowth

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

	if n := len(dir) + sockNameAllowance; n >= maxSocketPath {
		t.Fatalf("testutil: runtime dir %s leaves a %d byte socket path, over the %d byte limit", dir, n, maxSocketPath)
	}
	return filepath.Clean(dir)
}
