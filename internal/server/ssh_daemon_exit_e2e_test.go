package server

import (
	"context"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// sshExitRun is one real SSH client attached to one real daemon session, with
// everything the far end has written so far kept for the assertions.
type sshExitRun struct {
	daemon  *session.Daemon
	session *gossh.Session
	client  *gossh.Client
	name    string

	mu   sync.Mutex
	seen strings.Builder
	done chan error
}

// startSSHExitRun brings up a daemon, an SSH server in front of it, and a real
// SSH client attached to a session on it, and waits until the client has painted.
func startSSHExitRun(t *testing.T, name string) *sshExitRun {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping SSH integration test in short mode")
	}
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("SHELL", "/bin/sh")

	d := session.NewDaemon(&session.DaemonConfig{Version: "test", DisableAutoRestore: true})
	if err := d.Start(); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(d.Stop)

	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- StartSSHServer(ctx, &SSHServerConfig{
			Host:           "127.0.0.1",
			Port:           port,
			KeyPath:        filepath.Join(t.TempDir(), "host_key"),
			DefaultSession: name,
			Version:        "test",
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

	clientCfg := &gossh.ClientConfig{
		User:            "tuios",
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         2 * time.Second,
	}
	addr := net.JoinHostPort("127.0.0.1", port)
	var client *gossh.Client
	deadline := time.Now().Add(10 * time.Second)
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
	t.Cleanup(func() { _ = client.Close() })

	sess, err := client.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := sess.RequestPty("xterm-256color", 40, 120, gossh.TerminalModes{gossh.ECHO: 0}); err != nil {
		t.Fatalf("request pty: %v", err)
	}
	if err := sess.Shell(); err != nil {
		t.Fatalf("start shell: %v", err)
	}

	run := &sshExitRun{daemon: d, session: sess, client: client, name: name, done: make(chan error, 1)}
	go func() {
		buf := make([]byte, 8192)
		for {
			n, rerr := stdout.Read(buf)
			if n > 0 {
				run.mu.Lock()
				run.seen.Write(buf[:n])
				run.mu.Unlock()
			}
			if rerr != nil {
				return
			}
		}
	}()
	go func() { run.done <- sess.Wait() }()

	// The client has to be up before anything is taken away from it.
	run.waitForOutput(t, 15*time.Second, "the SSH client never painted a frame")
	return run
}

func (r *sshExitRun) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seen.String()
}

func (r *sshExitRun) waitForOutput(t *testing.T, d time.Duration, what string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if len(r.text()) > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s", what)
}

// waitForExit waits for the SSH session to close and returns everything the
// server wrote. A session that never closes is the bug: the user is left looking
// at a frame of a session that no longer exists.
func (r *sshExitRun) waitForExit(t *testing.T, d time.Duration) string {
	t.Helper()
	select {
	case err := <-r.done:
		if err != nil && err != io.EOF {
			// A non-zero exit or a closed channel is still an exit.
			t.Logf("ssh session ended: %v", err)
		}
	case <-time.After(d):
		t.Fatalf("the SSH client never exited: it is still rendering a session that is gone\n--- last output ---\n%s",
			tailPrintable(r.text()))
	}
	return r.text()
}

// tailPrintable keeps the last printable characters of a frame dump so a failure
// message is readable.
func tailPrintable(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\n' || (r >= ' ' && r < 0x7f) {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) > 2000 {
		out = out[len(out)-2000:]
	}
	return out
}

// TestSSHClientExitsWithAMessageWhenItsSessionIsKilled is the real thing: a real
// SSH client, over a real socket, attached to a real daemon session, and that
// session killed from another client.
func TestSSHClientExitsWithAMessageWhenItsSessionIsKilled(t *testing.T) {
	run := startSSHExitRun(t, "e2ekilled")

	killer := session.NewTUIClient()
	if err := killer.Connect("test", 80, 24); err != nil {
		t.Fatalf("killer connect: %v", err)
	}
	defer func() { _ = killer.Close() }()
	if err := killer.KillSessionByName(run.name); err != nil {
		t.Fatalf("kill session: %v", err)
	}

	out := tailPrintable(run.waitForExit(t, 20*time.Second))
	for _, want := range []string{"stopped", "Connect again"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the SSH client exited without saying %q:\n%s", want, out)
		}
	}
}

// TestSSHClientExitsWithAMessageWhenTheDaemonDies is the same run with the
// daemon taken away instead of the session.
func TestSSHClientExitsWithAMessageWhenTheDaemonDies(t *testing.T) {
	run := startSSHExitRun(t, "e2elost")

	run.daemon.Stop()

	out := tailPrintable(run.waitForExit(t, 20*time.Second))
	for _, want := range []string{"daemon", "Connect again"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the SSH client exited without saying %q:\n%s", want, out)
		}
	}
}
