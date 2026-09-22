package session

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// run-command is documented as the escape hatch for a keybinding with no verb,
// reachable by the name the keymap gives it. It routed whatever name it was
// given, and the client's executor ran an unknown name as nothing and reported
// success: run-command toggle_tiling said "command executed" and changed
// nothing, twice, against a live client. The daemon now resolves the name and
// refuses one it does not know.
//
// NEGATIVE CONTROLS, each run by mutating the shipped code and watching the
// named test fail:
//
//   - verbRunCommand routing p.Command without resolving it:
//     TestRunCommandTakesTheKeymapName fails saying the client was sent
//     toggle_tiling, and TestRunCommandRefusesAnUnknownName fails saying the
//     verb succeeded.
//   - handleExecuteCommand skipping the resolve:
//     TestExecuteCommandRefusesAnUnknownName fails saying the CLI path
//     reported success.

// routedCommand reads the one remote command the daemon routes to the fake TUI,
// answers it, and returns what was routed.
func routedCommand(t *testing.T, d *Daemon, sessionID string) (chan RemoteCommandPayload, *connState) {
	t.Helper()
	tui, clientSide := newFakeTUI(t, d, sessionID)
	routed := make(chan RemoteCommandPayload, 1)
	go func() {
		msg, err := ReadMessage(clientSide)
		if err != nil {
			return
		}
		var rc RemoteCommandPayload
		if err := msg.ParsePayload(&rc); err != nil {
			return
		}
		routed <- rc
		resMsg, err := NewMessage(MsgCommandResult, &CommandResultPayload{RequestID: rc.RequestID, Success: true, Message: "command executed"})
		if err != nil {
			return
		}
		_ = d.handleCommandResult(tui, resMsg)
	}()
	return routed, tui
}

func TestRunCommandTakesTheKeymapName(t *testing.T) {
	d := NewDaemon(&DaemonConfig{Version: "test", DisableAutoRestore: true})
	defer d.manager.Shutdown()
	sess, err := d.manager.CreateSession("keymap", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	routed, _ := routedCommand(t, d, sess.ID)

	out, verr := d.verbRunCommand(nil, json.RawMessage(`{"session":"keymap","command":"toggle_tiling"}`))
	if verr != nil {
		t.Fatalf("run-command toggle_tiling: %v", verr)
	}
	select {
	case rc := <-routed:
		if rc.TapeCommand != "ToggleTiling" {
			t.Fatalf("the client was sent %q, want ToggleTiling", rc.TapeCommand)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("nothing was routed to the client")
	}
	if got := out.(map[string]any)["command"]; got != "ToggleTiling" {
		t.Errorf("the result names %v, want the command that ran", got)
	}
}

func TestRunCommandRefusesAnUnknownName(t *testing.T) {
	d := NewDaemon(&DaemonConfig{Version: "test", DisableAutoRestore: true})
	defer d.manager.Shutdown()
	sess, err := d.manager.CreateSession("unknown", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	routed, _ := routedCommand(t, d, sess.ID)

	_, verr := d.verbRunCommand(nil, json.RawMessage(`{"session":"unknown","command":"toggle_zooom"}`))
	if verr == nil {
		t.Fatal("run-command toggle_zooom succeeded")
	}
	if verr.Code != ErrVerbInvalidParams || !strings.Contains(verr.Message, "toggle_zooom") || !strings.Contains(verr.Message, "--list") {
		t.Fatalf("error = %s %q, want an invalid-param error naming the command and --list", verr.Code, verr.Message)
	}
	select {
	case rc := <-routed:
		t.Fatalf("an unknown name was routed to the client as %q", rc.TapeCommand)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestExecuteCommandRefusesAnUnknownName is the CLI's road, which does not go
// through the verb.
func TestExecuteCommandRefusesAnUnknownName(t *testing.T) {
	d := NewDaemon(&DaemonConfig{Version: "test", DisableAutoRestore: true})
	defer d.manager.Shutdown()
	sess, err := d.manager.CreateSession("cli", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	cli, cliSide := newFakeTUI(t, d, sess.ID)
	msg, err := NewMessage(MsgExecuteCommand, &ExecuteCommandPayload{SessionName: "cli", CommandType: "toggle_zooom", RequestID: "req-1"})
	if err != nil {
		t.Fatal(err)
	}
	// The reply is written on the handler's goroutine into an unbuffered pipe,
	// so the handler runs beside the read.
	go func() { _ = d.handleExecuteCommand(cli, msg) }()
	reply, err := ReadMessage(cliSide)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Type != MsgCommandResult {
		t.Fatalf("the CLI path routed toggle_zooom to the client instead of refusing it (got message type %v)", reply.Type)
	}
	var res CommandResultPayload
	if err := reply.ParsePayload(&res); err != nil {
		t.Fatal(err)
	}
	if res.Success {
		t.Fatalf("the CLI path reported success for toggle_zooom: %+v", res)
	}
	if !strings.Contains(res.Message, "toggle_zooom") {
		t.Fatalf("the error does not name the command: %q", res.Message)
	}
}
