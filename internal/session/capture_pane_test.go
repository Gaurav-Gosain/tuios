package session

import (
	"net"
	"testing"
	"time"
)

// TestCapturePaneDoesNotDependOnAnAttachedClient pins that capture-pane is
// answered from the daemon's own VT emulator whether or not a TUI is watching.
//
// It used to route to an attached client and answer here only when nothing was
// attached, so the same read was served by two implementations and which one you
// got depended on whether someone happened to have the session open. The JSON
// verb path never routed, so the two protocols already disagreed about it.
func TestCapturePaneDoesNotDependOnAnAttachedClient(t *testing.T) {
	d := NewDaemon(&DaemonConfig{Version: "test", DisableAutoRestore: true})
	defer d.manager.Shutdown()

	sess, err := d.manager.CreateSession("cap", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	win, err := sess.AddDaemonWindow("w", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}

	pty := sess.GetPTY(win.PTYID)
	if pty == nil {
		t.Fatal("no PTY for the created window")
	}
	if _, err := pty.Write([]byte("echo tuios-capture-marker\n")); err != nil {
		t.Fatalf("write to PTY: %v", err)
	}

	waitForCapture := func() string {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if got := pty.CaptureContent(false, false); len(got) > 0 {
				return got
			}
			time.Sleep(20 * time.Millisecond)
		}
		return ""
	}
	if waitForCapture() == "" {
		t.Skip("shell produced no output in this environment")
	}

	// Attach a client. Before this change its presence redirected the read.
	newFakeTUI(t, d, sess.ID)

	// The caller is a separate connection that is not the TUI, which is what a
	// CLI invocation is. It must get a distinct client ID: sharing one would
	// replace the TUI in the daemon's client map and quietly test nothing.
	requesterServer, requesterSide := net.Pipe()
	requester := &connState{
		conn:             requesterServer,
		clientID:         "fake-cli",
		done:             make(chan struct{}),
		codec:            DefaultCodec(),
		ptySubscriptions: make(map[string]struct{}),
		sessionID:        sess.ID,
	}
	d.clientsMu.Lock()
	d.clients[requester.clientID] = requester
	d.clientsMu.Unlock()
	t.Cleanup(func() { _ = requesterSide.Close(); _ = requesterServer.Close() })

	if d.findTUIClient(sess.ID) == nil {
		t.Fatal("no TUI registered: the routed path this test rules out is unreachable")
	}

	msg, err := NewMessage(MsgCapturePane, &CapturePanePayload{
		RequestID:   "cap-1",
		SessionName: "cap",
	})
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}

	type reply struct {
		res *CommandResultPayload
		err error
	}
	replies := make(chan reply, 1)
	go func() {
		m, _, readErr := ReadMessageWithCodec(requesterSide)
		if readErr != nil {
			replies <- reply{err: readErr}
			return
		}
		var res CommandResultPayload
		replies <- reply{res: &res, err: m.ParsePayloadWithCodec(&res, DefaultCodec())}
	}()

	if err := d.handleCapturePane(requester, msg); err != nil {
		t.Fatalf("handleCapturePane: %v", err)
	}

	select {
	case r := <-replies:
		if r.err != nil {
			t.Fatalf("reading the reply: %v", r.err)
		}
		if !r.res.Success {
			t.Fatalf("capture failed: %s", r.res.Message)
		}
		if _, ok := r.res.Data["content"]; !ok {
			t.Fatal("reply carried no content")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("capture-pane sent no reply; it routed to the attached client instead of answering")
	}

	// Nothing was routed, so nothing is waiting on a client to answer.
	if n := pendingCount(d); n != 0 {
		t.Fatalf("pendingRequests = %d, want 0: the read was routed", n)
	}

}
