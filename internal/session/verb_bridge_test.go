package session

import (
	"encoding/json"
	"net"
	"testing"
	"time"
)

// newFakeTUI registers a connState that looks like an attached TUI client and
// returns it along with the client-side pipe end the test drives.
func newFakeTUI(t *testing.T, d *Daemon, sessionID string) (*connState, net.Conn) {
	t.Helper()
	serverSide, clientSide := net.Pipe()
	tui := &connState{
		conn:             serverSide,
		clientID:         "fake-tui",
		done:             make(chan struct{}),
		codec:            DefaultCodec(),
		ptySubscriptions: make(map[string]struct{}),
		sessionID:        sessionID,
		isTUIClient:      true,
	}
	d.clientsMu.Lock()
	d.clients[tui.clientID] = tui
	d.clientsMu.Unlock()
	t.Cleanup(func() { _ = clientSide.Close(); _ = serverSide.Close() })
	return tui, clientSide
}

// answerRemoteCommand reads the one remote command the daemon routes to the fake
// TUI and replies with the given result, mimicking what the real TUI does.
func answerRemoteCommand(t *testing.T, d *Daemon, tui *connState, clientSide net.Conn, result *CommandResultPayload) {
	go func() {
		msg, _, err := ReadMessageWithCodec(clientSide)
		if err != nil {
			return
		}
		var rc RemoteCommandPayload
		if err := msg.ParsePayloadWithCodec(&rc, DefaultCodec()); err != nil {
			return
		}
		result.RequestID = rc.RequestID
		resMsg, err := NewMessage(MsgCommandResult, result)
		if err != nil {
			return
		}
		_ = d.handleCommandResult(tui, resMsg)
	}()
}

func pendingCount(d *Daemon) int {
	d.pendingRequestsMu.RLock()
	defer d.pendingRequestsMu.RUnlock()
	return len(d.pendingRequests)
}

func TestRouteToTUISyncDeliversResult(t *testing.T) {
	d := NewDaemon(&DaemonConfig{Version: "test", DisableAutoRestore: true})
	defer d.manager.Shutdown()

	tui, clientSide := newFakeTUI(t, d, "sess-1")
	answerRemoteCommand(t, d, tui, clientSide, &CommandResultPayload{
		Success: true, Message: "done", Data: map[string]any{"window_id": "w-123"},
	})

	res, err := d.routeToTUISync(tui, "req-abc",
		&RemoteCommandPayload{CommandType: "tape_command", TapeCommand: "NewWindow"},
		3*time.Second)
	if err != nil {
		t.Fatalf("routeToTUISync: %v", err)
	}
	if !res.Success || res.Data["window_id"] != "w-123" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if n := pendingCount(d); n != 0 {
		t.Errorf("pending requests not cleaned up: %d", n)
	}
}

func TestRouteToTUISyncTimeout(t *testing.T) {
	d := NewDaemon(&DaemonConfig{Version: "test", DisableAutoRestore: true})
	defer d.manager.Shutdown()

	tui, clientSide := newFakeTUI(t, d, "sess-2")
	// Drain the command but never reply.
	go func() { _, _, _ = ReadMessageWithCodec(clientSide) }()

	_, err := d.routeToTUISync(tui, "req-timeout",
		&RemoteCommandPayload{CommandType: "send_keys", Keys: "x"},
		150*time.Millisecond)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if n := pendingCount(d); n != 0 {
		t.Errorf("pending requests not cleaned up after timeout: %d", n)
	}
}

// TestVerbNewWindowRoutesToAttachedTUI verifies that with a TUI attached the
// new-window verb routes to it (rather than mutating daemon-owned state), so the
// daemon and the live renderer stay in sync.
func TestVerbNewWindowRoutesToAttachedTUI(t *testing.T) {
	d := NewDaemon(&DaemonConfig{Version: "test", DisableAutoRestore: true})
	defer d.manager.Shutdown()

	sess, err := d.manager.CreateSession("routed", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	tui, clientSide := newFakeTUI(t, d, sess.ID)
	answerRemoteCommand(t, d, tui, clientSide, &CommandResultPayload{
		Success: true, Data: map[string]any{"window_id": "tui-win", "name": "build"},
	})

	requester := &connState{clientID: "ctl", done: make(chan struct{}), codec: DefaultCodec()}
	out, verr := d.verbNewWindow(requester, json.RawMessage(`{"session":"routed","name":"build"}`))
	if verr != nil {
		t.Fatalf("verbNewWindow: %v", verr)
	}
	m := out.(map[string]any)
	if m["window_id"] != "tui-win" {
		t.Fatalf("expected routed window_id, got %v", m)
	}
	// The routed path must not have created a daemon-owned window.
	if got := len(sess.GetState().Windows); got != 0 {
		t.Errorf("routed new-window mutated daemon state: %d windows", got)
	}
}

// TestVerbSetOptionRecordsAndRoutes verifies set-option records the value in
// daemon-owned state and reports applied=true when the attached TUI accepts it.
func TestVerbSetOptionRecordsAndRoutes(t *testing.T) {
	d := NewDaemon(&DaemonConfig{Version: "test", DisableAutoRestore: true})
	defer d.manager.Shutdown()

	sess, err := d.manager.CreateSession("cfg", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	tui, clientSide := newFakeTUI(t, d, sess.ID)
	answerRemoteCommand(t, d, tui, clientSide, &CommandResultPayload{Success: true})

	out, verr := d.verbSetOption(nil, json.RawMessage(`{"session":"cfg","key":"border_style","value":"double"}`))
	if verr != nil {
		t.Fatalf("verbSetOption: %v", verr)
	}
	m := out.(map[string]any)
	if m["applied"] != true {
		t.Errorf("applied = %v, want true", m["applied"])
	}
	if v, ok := sess.GetOption("border_style"); !ok || v != "double" {
		t.Errorf("option not recorded: %q,%v", v, ok)
	}
}
