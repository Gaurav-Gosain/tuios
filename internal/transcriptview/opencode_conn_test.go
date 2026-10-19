//go:build linux || darwin || windows

package transcriptview

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestOpenCodeConnOwner runs the check against the kernel's own socket
// table: /proc/net/tcp on Linux, the sysctl net.inet.tcp.pcblist_n on macOS,
// GetExtendedTcpTable and the process token on Windows. A connection to a
// listener of this process is this user's. The same connection, checked as
// another user, is not, and the password never goes out: the negative
// control for each system.
func TestOpenCodeConnOwner(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() })
		}
	}()
	dial := func(t *testing.T) net.Conn {
		t.Helper()
		c, err := net.Dial("tcp4", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.Run("this user's", func(t *testing.T) {
		if !connOwnedByMe(ctx, dial(t), os.Getpid()) {
			t.Fatal("a connection to this process was not shown to be this user's")
		}
	})
	t.Run("as another user", func(t *testing.T) {
		asOtherUser(t)
		if connOwnedByMe(ctx, dial(t), os.Getpid()) {
			t.Fatal("a connection was this user's under another user's id")
		}
	})
	if wantsServicePID {
		t.Run("another pid", func(t *testing.T) {
			if connOwnedByMe(ctx, dial(t), os.Getpid()+1) {
				t.Fatal("a connection was trusted with a pid other than its owner's")
			}
		})
	}

	page, err := os.ReadFile(filepath.Join("testdata", "opencode-messages.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Run("reads through the service", func(t *testing.T) {
		noOpenCode(t)
		f := newFakeOpenCode(t, page)
		p, err := Read(writeService(t, f.srv.URL, f.pid, 0o600), Options{Format: FormatOpenCode, Session: ocSession})
		if err != nil {
			t.Fatal(err)
		}
		checkOpenCodePage(t, p)
	})
	t.Run("no request to a service of another user", func(t *testing.T) {
		noOpenCode(t)
		f := newFakeOpenCode(t, page)
		asOtherUser(t)
		_, err := Read(writeService(t, f.srv.URL, f.pid, 0o600), Options{Format: FormatOpenCode, Session: ocSession})
		if err != ErrService {
			t.Fatalf("err %v, want ErrService", err)
		}
		if n := f.hits.Load(); n != 0 {
			t.Fatalf("%d requests carried the password to another user's service", n)
		}
	})
}
