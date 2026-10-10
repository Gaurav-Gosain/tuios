//go:build linux || darwin

package session

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The agent socket a split passes on comes from another process's
// environment. Ways it could be wrong: a link that points at another user's
// socket, or a socket in a folder another user can write to, where the name
// can be swapped. Only a socket of this user, in a folder only this user can
// write, is passed.
func TestOwnedSocket(t *testing.T) {
	listen := func(dir string) string {
		path := filepath.Join(dir, "s")
		l, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = l.Close() })
		return path
	}
	private := filepath.Join(t.TempDir(), "p")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	sock := listen(private)
	if !ownedSocket(sock) {
		t.Fatalf("a socket in a private folder was refused")
	}
	link := filepath.Join(private, "link")
	if err := os.Symlink(sock, link); err != nil {
		t.Fatal(err)
	}
	if ownedSocket(link) {
		t.Fatalf("a link to the socket was passed")
	}
	open := filepath.Join(t.TempDir(), "o")
	if err := os.Mkdir(open, 0o700); err != nil {
		t.Fatal(err)
	}
	openSock := listen(open)
	if err := os.Chmod(open, 0o777); err != nil {
		t.Fatal(err)
	}
	if ownedSocket(openSock) {
		t.Fatalf("a socket in a folder others can write to was passed")
	}
	file := filepath.Join(private, "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if ownedSocket(file) || ownedSocket("relative/s") {
		t.Fatalf("a plain file or a relative path was passed")
	}
}

// A symlink ancestor sits in every standard macOS temp path (/var, a symlink
// to /private/var), so rejecting any path that passes through one would
// refuse the socket in the common case, not just the suspicious one.
// ownedSocket resolves the socket's folder once up front, so the walk itself
// never sees the symlink — it checks whatever the symlink resolves to, the
// same as a real directory at that position would be checked, and still
// rejects a resolved target that is not actually safe.
func TestOwnedSocketFollowsASafeSymlinkAncestor(t *testing.T) {
	listen := func(dir string) string {
		path := filepath.Join(dir, "s")
		l, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = l.Close() })
		return path
	}
	// A short-named root, not t.TempDir(): a Unix domain socket's sun_path is
	// capped (104 bytes on macOS/BSD), and t.TempDir() embeds this test's own
	// (long) function name in the directory it hands back.
	root, err := os.MkdirTemp("", "owned-sock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })

	safeTarget := filepath.Join(root, "real")
	if err := os.Mkdir(safeTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	safeLink := filepath.Join(root, "link-to-real")
	if err := os.Symlink(safeTarget, safeLink); err != nil {
		t.Fatal(err)
	}
	if sock := listen(safeTarget); !ownedSocket(filepath.Join(safeLink, "s")) {
		t.Fatalf("a socket reached through a symlink to a safe folder was refused: %s", sock)
	}

	unsafeTarget := filepath.Join(root, "unsafe")
	if err := os.Mkdir(unsafeTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	// Mkdir's mode is masked by umask; Chmod sets the exact bits, same as
	// TestOwnedSocket's "open" case above does for the same reason.
	if err := os.Chmod(unsafeTarget, 0o777); err != nil {
		t.Fatal(err)
	}
	unsafeLink := filepath.Join(root, "link-to-unsafe")
	if err := os.Symlink(unsafeTarget, unsafeLink); err != nil {
		t.Fatal(err)
	}
	if sock := listen(unsafeTarget); ownedSocket(filepath.Join(unsafeLink, "s")) {
		t.Fatalf("a socket reached through a symlink to a world-writable folder was passed: %s", sock)
	}
}

// ssh -G reads ~/.ssh/config, where a Match exec can start a child that keeps
// ssh's output open. The lookup must still return at its timeout.
func TestResolveSSHHostNameIsBounded(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "ssh")
	script := "#!/bin/sh\nsleep 30 &\nsleep 30\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	got := resolveSSHHostName(remoteLogin{bin: bin, dest: "h"})
	if took := time.Since(start); took > sshResolveTimeout+2*time.Second {
		t.Fatalf("ssh -G took %v, want at most about %v", took, sshResolveTimeout)
	}
	if got != "" {
		t.Fatalf("got %q from an ssh that printed nothing", got)
	}
}
