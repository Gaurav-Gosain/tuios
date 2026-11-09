package session

import (
	"errors"
	"net"
	"testing"
	"time"
)

// TestDaemonActionsFollowTheClientsHello (T7, wire compatibility): the daemon
// runs a batch A action itself with a TUI attached only while every attached
// TUI said DaemonActions in its hello. A TUI from before it runs the action
// in its own state when its key is pressed and pushes the result, so for it
// the daemon routes the action as it always did: the TUI gets the remote
// command and the daemon's state does not change until it answers. A TUI
// that says DaemonActions gets no remote command and the daemon's state
// changes. Mixed versions happen on every daemon upgrade, which outlives the
// client by design.
//
// How this could pass wrongly, written down first:
//   - A client could get no remote command because it read too little: the
//     new client reads every frame for a while after the result, and the old
//     one must get the command before any result reaches the caller.
//   - The daemon could run the action and also route it: for the old client
//     the state is read before it answers, and must be unchanged.
func TestDaemonActionsFollowTheClientsHello(t *testing.T) {
	for _, tc := range []struct {
		name   string
		hello  bool
		routed bool
	}{
		{"a TUI from before DaemonActions", false, true},
		{"a TUI that sends daemon actions", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, sp := startTestDaemon(t)
			sess := makeSessionWithWindow(t, d, "acts")
			tui := dialActionsClient(t, sp, tc.hello)
			tui.send(t, MsgAttach, &AttachPayload{SessionName: "acts", Width: 120, Height: 40})
			tui.await(t, MsgAttached)
			cli := dialActionsClient(t, sp, false)

			for _, action := range []string{"minimize_window", "restore_all"} {
				cli.send(t, MsgExecuteCommand, &ExecuteCommandPayload{SessionName: "acts", CommandType: action, RequestID: action})
				if tc.routed {
					var rc RemoteCommandPayload
					if err := tui.await(t, MsgRemoteCommand).ParsePayload(&rc); err != nil {
						t.Fatalf("%s: parse remote command: %v", action, err)
					}
					if got := sess.GetState().Windows[0].Minimized; got != (action == "restore_all") {
						t.Fatalf("%s: the daemon changed minimized to %v itself and routed it too", action, got)
					}
					tui.send(t, MsgCommandResult, &CommandResultPayload{RequestID: rc.RequestID, Success: true})
				}
				var res CommandResultPayload
				if err := cli.await(t, MsgCommandResult).ParsePayload(&res); err != nil {
					t.Fatalf("%s: parse result: %v", action, err)
				}
				if !res.Success {
					t.Fatalf("%s: failed: %s", action, res.Message)
				}
				if tc.routed {
					// The old client was the one to change it, by its push;
					// this one did not, so the state is as it was.
					if err := sess.SetDaemonWindowMinimized(sess.GetState().Windows[0].ID, action == "minimize_window"); err != nil {
						t.Fatalf("%s: stand in for the old client's push: %v", action, err)
					}
					continue
				}
				if got := sess.GetState().Windows[0].Minimized; got != (action == "minimize_window") {
					t.Fatalf("%s: the daemon did not run it: minimized %v", action, got)
				}
				if tui.sawRemoteCommand(t, 300*time.Millisecond) {
					t.Fatalf("%s: routed to a TUI that sends daemon actions", action)
				}
			}
		})
	}
}

// dialActionsClient says hello as a TUI that sends daemon actions or not.
func dialActionsClient(t *testing.T, socketPath string, actions bool) *boundsClient {
	t.Helper()
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	c := &boundsClient{conn: conn}
	c.send(t, MsgHello, &HelloPayload{Version: "test", PreferredCodec: "gob", Protocol: ProtocolVersion, LayoutTreeOps: true, DaemonActions: actions})
	var w WelcomePayload
	if err := c.await(t, MsgWelcome).ParsePayload(&w); err != nil {
		t.Fatalf("parse welcome: %v", err)
	}
	if !w.DaemonActions {
		t.Fatalf("the welcome did not offer DaemonActions")
	}
	return c
}

// sawRemoteCommand reads frames for d and reports whether one was a remote
// command.
func (c *boundsClient) sawRemoteCommand(t *testing.T, d time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		_ = c.conn.SetReadDeadline(deadline)
		msg, err := ReadMessage(c.conn)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return false
			}
			t.Fatalf("read: %v", err)
		}
		if msg.Type == MsgRemoteCommand {
			return true
		}
	}
}
