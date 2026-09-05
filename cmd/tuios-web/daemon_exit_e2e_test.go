package main

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/sip"
	"github.com/coder/websocket"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The sip wire protocol, as the browser speaks it. The first byte of every
// frame is the type; the rest is the payload.
const (
	wsOutput = '1'
	wsResize = '2'
	wsClose  = '7'
)

// webExitRun is a running daemon, a real sip server in front of it, and a
// websocket client where the browser would be, holding everything the server has
// written to the terminal so far.
type webExitRun struct {
	daemon *session.Daemon
	name   string

	mu     sync.Mutex
	seen   strings.Builder
	closed bool
	done   chan struct{}
}

// startWebExitRun brings the whole stack up and waits until the client has
// painted its first frame.
func startWebExitRun(t *testing.T, name string) *webExitRun {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("SHELL", "/bin/sh")

	d := session.NewDaemon(&session.DaemonConfig{Version: "test", DisableAutoRestore: true})
	if err := d.Start(); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(d.Stop)

	webServerConfig.defaultSession = name
	webServerConfig.ephemeral = false
	webServerConfig.version = "test"

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	_, port, _ := net.SplitHostPort(l.Addr().String())
	_ = l.Close()

	cfg := sip.DefaultConfig()
	cfg.Host = "127.0.0.1"
	cfg.Port = port
	cfg.AllowInsecureNoTLS = true

	ctx, cancel := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)
	go func() { serveErr <- sip.NewServer(cfg).ServeWithProgram(ctx, createTUIOSProgram) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-serveErr:
		case <-time.After(5 * time.Second):
			t.Log("web server did not shut down within timeout")
		}
	})

	url := "ws://127.0.0.1:" + port + "/ws"
	var conn *websocket.Conn
	deadline := time.Now().Add(10 * time.Second)
	for {
		c, _, derr := websocket.Dial(ctx, url, nil)
		if derr == nil {
			conn = c
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("could not connect to the web server: %v", derr)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })

	// The browser's first frame is its size, and the server waits for it before
	// it makes the session.
	size, _ := json.Marshal(map[string]int{"cols": 120, "rows": 40})
	if err := conn.Write(ctx, websocket.MessageBinary, append([]byte{wsResize}, size...)); err != nil {
		t.Fatalf("send initial size: %v", err)
	}

	run := &webExitRun{daemon: d, name: name, done: make(chan struct{})}
	go func() {
		defer close(run.done)
		for {
			_, data, rerr := conn.Read(ctx)
			if rerr != nil {
				return
			}
			if len(data) == 0 {
				continue
			}
			switch data[0] {
			case wsOutput:
				run.mu.Lock()
				run.seen.Write(data[1:])
				run.mu.Unlock()
			case wsClose:
				run.mu.Lock()
				run.closed = true
				run.mu.Unlock()
				return
			}
		}
	}()

	painted := time.Now().Add(15 * time.Second)
	for {
		if len(run.text()) > 0 {
			break
		}
		if time.Now().After(painted) {
			t.Fatalf("the browser client never painted a frame")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return run
}

func (r *webExitRun) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seen.String()
}

// waitForClose waits for the server to tell the browser the session is over. A
// session that never closes is the bug: the tab keeps its last frame for ever.
func (r *webExitRun) waitForClose(t *testing.T, d time.Duration) string {
	t.Helper()
	select {
	case <-r.done:
	case <-time.After(d):
		t.Fatalf("the browser client was never told the session ended: it is still showing a session that is gone\n--- last output ---\n%s",
			printable(r.text()))
	}
	return printable(r.text())
}

// printable keeps the readable characters of a terminal stream so a failure
// message can be read.
func printable(s string) string {
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

// TestWebClientEndsWithAMessageWhenItsSessionIsKilled drives the real web stack:
// a browser's websocket, sip, the TUI, and a daemon session killed underneath it.
func TestWebClientEndsWithAMessageWhenItsSessionIsKilled(t *testing.T) {
	run := startWebExitRun(t, "webe2ekilled")

	killer := session.NewTUIClient()
	if err := killer.Connect("test", 80, 24); err != nil {
		t.Fatalf("killer connect: %v", err)
	}
	defer func() { _ = killer.Close() }()
	if err := killer.KillSessionByName(run.name); err != nil {
		t.Fatalf("kill session: %v", err)
	}

	out := run.waitForClose(t, 20*time.Second)
	for _, want := range []string{"stopped", "Connect again"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the browser client ended without saying %q:\n%s", want, out)
		}
	}
}

// TestWebClientEndsWithAMessageWhenTheDaemonDies is the same run with the daemon
// taken away instead of the session.
func TestWebClientEndsWithAMessageWhenTheDaemonDies(t *testing.T) {
	run := startWebExitRun(t, "webe2elost")

	run.daemon.Stop()

	out := run.waitForClose(t, 20*time.Second)
	for _, want := range []string{"daemon", "Connect again"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the browser client ended without saying %q:\n%s", want, out)
		}
	}
}
