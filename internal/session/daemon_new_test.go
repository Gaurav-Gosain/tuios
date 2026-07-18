package session

import (
	"net"
	"testing"
	"time"
)

// runHandleNew drives handleNew over a pipe, draining the SessionList response
// the handler writes so the send does not block.
func runHandleNew(t *testing.T, d *Daemon, payload *NewPayload) error {
	t.Helper()
	server, clientConn := net.Pipe()
	cs := &connState{
		conn:             clientConn,
		clientID:         "test-client",
		done:             make(chan struct{}),
		codec:            DefaultCodec(),
		ptySubscriptions: make(map[string]struct{}),
	}

	// Drain whatever the handler sends back.
	go func() {
		_ = server.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _, _ = ReadMessageWithCodec(server)
		_ = server.Close()
	}()

	msg, err := NewMessageWithCodec(MsgNew, payload, cs.codec)
	if err != nil {
		t.Fatalf("NewMessageWithCodec failed: %v", err)
	}
	err = d.handleNew(cs, msg)
	_ = clientConn.Close()
	return err
}

// TestHandleNewDetachSpawnsWindow verifies a detached session is created with an
// initial live window, so it is usable headless right away.
func TestHandleNewDetachSpawnsWindow(t *testing.T) {
	d := NewDaemon(&DaemonConfig{})
	defer d.manager.Shutdown()

	if err := runHandleNew(t, d, &NewPayload{SessionName: "detached", Width: 80, Height: 24, Detach: true}); err != nil {
		t.Fatalf("handleNew(detach) failed: %v", err)
	}

	sess := d.manager.GetSession("detached")
	if sess == nil {
		t.Fatal("detached session was not created")
	}
	state := sess.GetState()
	if len(state.Windows) != 1 {
		t.Fatalf("detached session window count = %d, want 1", len(state.Windows))
	}
	if state.FocusedWindowID == "" {
		t.Error("detached session has no focused window")
	}
	if pty := sess.GetPTY(state.Windows[0].PTYID); pty == nil {
		t.Error("detached session's initial window has no live PTY")
	}
}

// TestHandleNewWithoutDetachIsEmpty verifies the historical behavior is
// unchanged: a plain 'new' creates an empty session the TUI later populates.
func TestHandleNewWithoutDetachIsEmpty(t *testing.T) {
	d := NewDaemon(&DaemonConfig{})
	defer d.manager.Shutdown()

	if err := runHandleNew(t, d, &NewPayload{SessionName: "plain", Width: 80, Height: 24}); err != nil {
		t.Fatalf("handleNew failed: %v", err)
	}

	sess := d.manager.GetSession("plain")
	if sess == nil {
		t.Fatal("session was not created")
	}
	if len(sess.GetState().Windows) != 0 {
		t.Errorf("plain session window count = %d, want 0", len(sess.GetState().Windows))
	}
}
