package server

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// freePort asks the OS for an unused localhost TCP port and returns it. There is
// a small window between closing the listener and the server rebinding, which is
// acceptable for a test.
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer func() { _ = l.Close() }()
	_, port, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	return port
}

// TestSSHServer_AcceptsPTYSessionAndRenders starts the SSH server on localhost
// with a throwaway generated host key, connects with a real SSH client
// requesting a PTY, and asserts the TUIOS instance comes up and paints
// something. This exercises teaHandler end to end: capability detection,
// ephemeral instance creation, and the bubbletea program running over SSH.
func TestSSHServer_AcceptsPTYSessionAndRenders(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SSH integration test in short mode")
	}

	port := freePort(t)
	hostKey := filepath.Join(t.TempDir(), "test_host_key")

	ctx, cancel := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- StartSSHServer(ctx, &SSHServerConfig{
			Host:      "127.0.0.1",
			Port:      port,
			KeyPath:   hostKey,
			Ephemeral: true, // no daemon: keep the test self-contained
			Version:   "test",
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-serveErr:
		case <-time.After(5 * time.Second):
			t.Log("server did not shut down within timeout")
		}
	})

	// Dial until the listener is up (host key generation + bind take a moment).
	clientCfg := &gossh.ClientConfig{
		User: "tester",
		// No keys are configured in the isolated test tree, so this loopback
		// server runs unauthenticated. See ssh_auth_test.go for the servers
		// that do check.
		Auth:            nil,
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         2 * time.Second,
	}
	addr := net.JoinHostPort("127.0.0.1", port)

	var client *gossh.Client
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := gossh.Dial("tcp", addr, clientCfg)
		if err == nil {
			client = c
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("could not connect to SSH server: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer func() { _ = client.Close() }()

	sess, err := client.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer func() { _ = sess.Close() }()

	// A kitty client, so capability detection has something to enable.
	if err := sess.Setenv("TERM_PROGRAM", "ghostty"); err != nil {
		// Setenv may be rejected if the server does not accept env; not fatal.
		t.Logf("setenv rejected (ok): %v", err)
	}

	modes := gossh.TerminalModes{gossh.ECHO: 0}
	if err := sess.RequestPty("xterm-kitty", 24, 80, modes); err != nil {
		t.Fatalf("request pty: %v", err)
	}

	stdout, err := sess.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}

	if err := sess.Shell(); err != nil {
		t.Fatalf("start shell: %v", err)
	}

	// The TUI should paint an initial frame promptly. Read whatever arrives
	// within a generous window and assert it is non-empty.
	got := make(chan int, 1)
	go func() {
		buf := make([]byte, 4096)
		total := 0
		for total == 0 {
			n, rerr := stdout.Read(buf)
			total += n
			if rerr != nil {
				break
			}
		}
		got <- total
	}()

	select {
	case n := <-got:
		if n == 0 {
			t.Fatal("TUIOS produced no output over SSH")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("timed out waiting for TUIOS output over SSH")
	}
}
