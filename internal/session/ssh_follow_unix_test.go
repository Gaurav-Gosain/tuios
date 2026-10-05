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
