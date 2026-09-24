package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRuntimeDirHoldsEverySocketTuiosBinds builds an isolation root whose
// runtime directory has room for the daemon's tuios.sock and no room for a
// tmux shim pane holder's socket, and checks xdgDir moves it. Under a short
// TMPDIR such a root is common: the daemon then starts, the holder cannot
// bind, and respawn-pane reports the pane has no holder, which is how
// TestTmuxShimOpensATeammatePane failed when the check measured tuios.sock
// alone.
func TestRuntimeDirHoldsEverySocketTuiosBinds(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "rt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })

	// Pad the root so the daemon socket lands exactly on the limit.
	daemonSock := filepath.Join("XDG_RUNTIME_DIR", "tuios", "tuios.sock")
	pad := unixSocketPathMax - len(filepath.Join(root, "b", daemonSock))
	if pad < 0 {
		t.Fatalf("the temp root %s is already too long to build the case", root)
	}
	base := filepath.Join(root, "b"+strings.Repeat("x", pad))
	if got := len(filepath.Join(base, daemonSock)); got != unixSocketPathMax {
		t.Fatalf("built a daemon socket of %d bytes, want %d", got, unixSocketPathMax)
	}
	mustMkdir(base)

	dir := xdgDir(base, "XDG_RUNTIME_DIR")
	holder := filepath.Join(dir, "tuios", "tmux", "p", "2147483647.sock")
	if len(holder) > unixSocketPathMax {
		t.Errorf("xdgDir gave %s, where a pane holder's socket is %d bytes, over the %d the kernel takes", dir, len(holder), unixSocketPathMax)
	}
}
