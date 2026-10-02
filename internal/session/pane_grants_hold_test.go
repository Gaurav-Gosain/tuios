//go:build linux || darwin

package session

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestAPermissionReloadOnlyTightens: a pane that can write config.toml must
// not widen what panes hold by editing it. A reload that narrows applies at
// once; one that widens waits for the daemon to restart.
//
// Negative control: with onConfigReload applying the table as read, the
// pane holds admin after the reload to open.
func TestAPermissionReloadOnlyTightens(t *testing.T) {
	d, sp, a1, _, _ := scopeFixture(t)
	strictRead := &config.UserConfig{Agents: config.AgentsConfig{Permissions: config.PermissionsConfig{Mode: "strict", Grants: []string{"read"}}}}

	// open -> strict read narrows, so it applies.
	d.onConfigReload(strictRead, nil)
	if g, _ := d.manager.grants.effective(a1); g != GrantRead {
		t.Fatalf("after a narrowing reload the pane holds %v, want read", g)
	}

	// strict read -> open widens, so it waits.
	d.onConfigReload(&config.UserConfig{}, nil)
	if g, _ := d.manager.grants.effective(a1); g != GrantRead {
		t.Fatalf("after a reload to open the pane holds %v, want read until a restart", g)
	}
	if !d.manager.grants.strict() {
		t.Error("a reload to open switched strict off")
	}
	got := result(t, callP(dialVerb(t, sp), t, "pane-grants", nil))
	if got["restart_needed"] != true {
		t.Errorf("pane-grants does not say a change waits for a restart: %v", got)
	}

	// strict read -> strict read,write,respond widens too.
	d.onConfigReload(&config.UserConfig{Agents: config.AgentsConfig{Permissions: config.PermissionsConfig{Mode: "strict", Grants: []string{"read", "write", "respond"}}}}, nil)
	if g, _ := d.manager.grants.effective(a1); g != GrantRead {
		t.Fatalf("after a widening strict reload the pane holds %v, want read", g)
	}

	// strict read -> strict none narrows again, and applies.
	d.onConfigReload(&config.UserConfig{Agents: config.AgentsConfig{Permissions: config.PermissionsConfig{Mode: "strict", Grants: []string{}}}}, nil)
	if g, _ := d.manager.grants.effective(a1); g != 0 {
		t.Fatalf("after a narrowing reload to none the pane holds %v, want none", g)
	}

	// A mixed change applies what it takes away and waits for what it adds.
	d2, _, b1, _, _ := scopeFixture(t)
	d2.manager.SetPanePermissions(config.ResolvedPermissions{Strict: true, Grants: []string{"read", "write"}})
	d2.onConfigReload(&config.UserConfig{Agents: config.AgentsConfig{Permissions: config.PermissionsConfig{Mode: "strict", Grants: []string{"read", "fan"}}}}, nil)
	if g, _ := d2.manager.grants.effective(b1); g != GrantRead {
		t.Fatalf("after a mixed reload the pane holds %v, want read", g)
	}
}

// deadPID is the pid of a process that has exited and been reaped.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run true: %v", err)
	}
	return cmd.Process.Pid
}

// TestAnUnreadableCallerIsHeldToTheStrictDefault: a caller whose process
// cannot be read, such as one that connected, handed the socket to a child
// and exited, is counted as inside a pane. It is held to the strict default
// in no session, not passed as the person.
//
// Negative control: with paneAuthority returning nil for such a caller, the
// send-text below is passed.
func TestAnUnreadableCallerIsHeldToTheStrictDefault(t *testing.T) {
	skipWithoutPeerPID(t)
	d, _, a1, _, b1 := scopeFixture(t)
	setStrict(d)
	cs := &connState{clientID: "gone", peerPID: deadPID(t)}
	if _, verr := d.checkGrants(cs, "send-text", json.RawMessage(`{"session":"b","window":"`+b1+`","text":"x"}`)); verr == nil || verr.Code != ErrVerbForbidden {
		t.Fatalf("send-text from a caller that cannot be read = %v, want forbidden", verr)
	}
	if verr := d.checkGrantMessage(&connState{clientID: "gone2", peerPID: deadPID(t)}, MsgAttach); verr == nil {
		t.Error("a caller that cannot be read may use the client protocol")
	}
	// Under open with one narrowed pane, the trick must not turn a narrowed
	// pane into admin either.
	d.manager.SetPanePermissions(config.ResolvedPermissions{})
	g := GrantRead
	d.manager.grants.set(a1, &g)
	if _, verr := d.checkGrants(&connState{clientID: "gone3", peerPID: deadPID(t)}, "kill-session", json.RawMessage(`{"session":"b"}`)); verr == nil {
		t.Error("kill-session from a caller that cannot be read was passed under open")
	}
}

// TestADaemonChildOutsideEveryPaneIsHeld: a process the daemon starts outside
// every pane shell, such as what ssh runs for a ProxyCommand, is counted as
// inside the daemon's panes and placed in none. Under strict it holds the
// grants list in no session, so it cannot give a pane more.
//
// Negative control: with paneAuthority returning nil for such a caller, the
// set-pane-grants below is served and pane a1 holds admin and respond.
func TestADaemonChildOutsideEveryPaneIsHeld(t *testing.T) {
	skipWithoutPeerPID(t)
	d, sp := startTestDaemon(t)
	setStrict(d, "read")
	_, a, _ := twoWindowSession(t, d, "child")

	out := filepath.Join(t.TempDir(), "out")
	req := `{"id":1,"verb":"set-pane-grants","params":{"session":"child","window":"` + a + `","grants":["admin","respond"]}}`
	// The test process is the daemon, so a child of it is a daemon child
	// that no pane shell started.
	cmd := exec.Command("/bin/sh", "-c", helperCommand(t, sp, out, "send", req))
	env := []string{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "TUIOS_") {
			env = append(env, kv)
		}
	}
	cmd.Env = env
	if err := cmd.Run(); err != nil {
		t.Fatalf("sh: %v", err)
	}
	var resp map[string]any
	if raw := waitHelper(t, out); json.Unmarshal([]byte(raw), &resp) != nil {
		t.Fatalf("helper said %q", raw)
	}
	if e, _ := resp["error"].(map[string]any); e == nil || e["code"] != ErrVerbForbidden {
		t.Fatalf("a daemon child outside every pane set a pane's grants: %v", resp)
	}
	if g, _ := d.manager.grants.effective(a); g != GrantRead {
		t.Errorf("pane a holds %v, want read", g)
	}
}

// TestAConnectionWhoseProcessChangedIsHeld: the pid a connection was made
// from is pinned with the process's start time at accept. When the pid names
// another process later, as after the caller handed the connection to a
// child, exited and had its pid reused, nothing read about the pid is taken
// as the caller's.
//
// Negative control: with the peerChanged check cut from paneAuthority, the
// caller below is read as the test runner, outside every pane, and passed.
func TestAConnectionWhoseProcessChangedIsHeld(t *testing.T) {
	skipWithoutPeerPID(t)
	d, _, _, _, b1 := scopeFixture(t)
	setStrict(d)
	// The parent of this test binary is outside every pane. A start time it
	// does not have stands for a process that held its pid before.
	cs := &connState{clientID: "reused", peerPID: os.Getppid(), peerStart: 1, peerStartOK: true}
	if _, verr := d.checkGrants(cs, "send-text", json.RawMessage(`{"session":"b","window":"`+b1+`","text":"x"}`)); verr == nil || verr.Code != ErrVerbForbidden {
		t.Fatalf("send-text from a connection whose process changed = %v, want forbidden", verr)
	}
	if !d.connFromPane(&connState{clientID: "reused2", peerPID: os.Getppid(), peerStart: 1, peerStartOK: true}) {
		t.Error("a connection whose process changed may act as the person")
	}
	// The same process, pinned right, is the person.
	live := &connState{clientID: "live", peerPID: os.Getppid()}
	d.pinPeer(live)
	if _, verr := d.checkGrants(live, "send-text", json.RawMessage(`{"session":"b","window":"`+b1+`","text":"x"}`)); verr != nil {
		t.Errorf("send-text from the person's live process = %v", verr)
	}
}

// TestAPaneCannotApplyConfig: apply-config is for the person. A pane holding
// admin, under the default open mode, is refused.
func TestAPaneCannotApplyConfig(t *testing.T) {
	skipWithoutPeerPID(t)
	d, sp := startTestDaemon(t)
	d.configPath = filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(d.configPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	sess, _, b := twoWindowSession(t, d, "apply")
	out := filepath.Join(t.TempDir(), "out")
	runInPane(t, d, sess, b, helperCommand(t, sp, out, "send", `{"id":1,"verb":"apply-config"}`))
	var resp map[string]any
	if raw := waitHelper(t, out); json.Unmarshal([]byte(raw), &resp) != nil {
		t.Fatalf("helper said %q", raw)
	}
	if e, _ := resp["error"].(map[string]any); e == nil || e["code"] != ErrVerbForbidden {
		t.Fatalf("a pane applied config.toml: %v", resp)
	}
}

// paneText reads what window shows now.
func paneText(t *testing.T, c *verbConn, session, window string) string {
	t.Helper()
	res := result(t, callP(c, t, "capture-pane", map[string]any{"session": session, "window": window}))
	return fmt.Sprint(res["content"], res["lines"], res["text"])
}

// waitPaneText waits up to five seconds for want in window.
func waitPaneText(c *verbConn, t *testing.T, session, window, want string) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(paneText(t, c, session, window), want) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// TestAPaneSendKeysNeverDrivesTheClient: send-keys with no window hands the
// keys to the attached client, where PREFIX moves focus and opens the Inbox,
// so the keys after it land somewhere no check saw. From a pane without
// respond, admin included, the keys go to the focused pane's terminal
// instead, and a PREFIX is refused.
//
// Negative control: with paneTypesRaw back to holding only panes without
// admin, the first send-keys is answered sent_to client.
func TestAPaneSendKeysNeverDrivesTheClient(t *testing.T) {
	d, sp, a1, _, _ := scopeFixture(t)
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	attachTUI(t, sp, "a")
	if err := d.manager.GetSession("a").mutateState(func(st *SessionState) error { st.FocusedWindowID = a1; return nil }); err != nil {
		t.Fatal(err)
	}
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	c := dialVerb(t, sp)
	got := result(t, callP(c, t, "send-keys", map[string]any{"session": "a", "keys": "x"}))
	if got["sent_to"] == "client" {
		t.Fatalf("an admin pane's send-keys went through the client: %v", got)
	}
	resp := callP(c, t, "send-keys", map[string]any{"session": "a", "keys": "PREFIX n"})
	wantForbidden(t, "an admin pane's PREFIX", resp)
	if e, _ := resp["error"].(map[string]any); e != nil {
		msg := fmt.Sprint(e["message"], e["hint"])
		if !strings.Contains(msg, "PREFIX is refused from a pane") || !strings.Contains(msg, "focus-window") {
			t.Errorf("the PREFIX refusal %q does not say it is refused and what to use", msg)
		}
	}
}

// TestAPaneRunCommandCannotType: run-command sends tape to the attached
// client, which types into whatever pane is focused when each command runs.
// From a pane without respond, admin included, a tape that types or presses
// keys is refused. One that does not is passed on.
//
// Negative control: with refuseTapeTyping cut from handleExecuteCommand, the
// script goes to the client and no refusal comes back. With it cut from
// verbRunCommand, the verb's Type goes to the client.
func TestAPaneRunCommandCannotType(t *testing.T) {
	d, sp, a1, _, _ := scopeFixture(t)
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	attachTUI(t, sp, "a")
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	run := func(p ExecuteCommandPayload) *CommandResultPayload {
		t.Helper()
		conn, err := net.DialTimeout("unix", sp, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		msg, err := NewMessage(MsgExecuteCommand, &p)
		if err != nil {
			t.Fatal(err)
		}
		if err := WriteMessage(conn, msg); err != nil {
			t.Fatal(err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		resp, err := ReadMessage(conn)
		if err != nil {
			return nil
		}
		var res CommandResultPayload
		if resp.Type != MsgCommandResult || resp.ParsePayload(&res) != nil {
			return nil
		}
		return &res
	}
	for _, p := range []ExecuteCommandPayload{
		{SessionName: "a", TapeScript: "NextWindow\nType \"1\"\nEnter\n", RequestID: "r1"},
		{SessionName: "a", CommandType: "Enter", RequestID: "r2"},
		{SessionName: "a", CommandType: "LoadLayout", Args: []string{"x"}, RequestID: "r3"},
	} {
		res := run(p)
		if res == nil || res.Success || !strings.Contains(res.Message, "respond grant") {
			t.Errorf("run-command %+v from an admin pane = %+v, want a refusal naming the respond grant", p, res)
		}
	}
	// The JSON verb runs the same tape commands through the client.
	c := dialVerb(t, sp)
	for _, params := range []map[string]any{
		{"session": "a", "command": "Type", "args": []string{"1"}},
		{"session": "a", "command": "Enter"},
		{"session": "a", "command": "KeyCombo", "args": []string{"ctrl+b"}},
	} {
		resp := callP(c, t, "run-command", params)
		wantForbidden(t, fmt.Sprint("run-command verb ", params["command"]), resp)
		if e, _ := resp["error"].(map[string]any); e != nil && !strings.Contains(fmt.Sprint(e["message"]), "respond grant") {
			t.Errorf("run-command verb refusal %v does not name the respond grant", e["message"])
		}
	}
}

// TestAPaneSetMultifocusOnlyNamesPanesItMayTypeInto: the windows in the
// multifocus set get every key the person types, so a pane may put a window
// there only when it may type into that window, as with send-keys.
//
// Negative control: with refuseMultifocusInto cut from verbRunCommand, the
// call goes to the client and no refusal comes back.
func TestAPaneSetMultifocusOnlyNamesPanesItMayTypeInto(t *testing.T) {
	d, sp, a1, a2, _ := scopeFixture(t)
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	attachTUI(t, sp, "a")
	if err := d.manager.GetSession("a").mutateState(func(st *SessionState) error {
		for i := range st.Windows {
			if st.Windows[i].ID == a2 {
				st.Windows[i].AgentState = AgentStateNeedsInput
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	c := dialVerb(t, sp)
	resp := callP(c, t, "run-command", map[string]any{"session": "a", "command": "SetMultifocus", "args": []string{a1, a2}})
	wantForbidden(t, "SetMultifocus naming a pane on a prompt", resp)
	if e, _ := resp["error"].(map[string]any); e == nil || !strings.Contains(fmt.Sprint(e["message"]), "waiting on a prompt") {
		t.Errorf("the refusal %v does not say the pane waits on a prompt", resp["error"])
	}
	resp = callP(c, t, "run-command", map[string]any{"session": "a", "command": "SetMultifocus", "args": []string{"no-such-window"}})
	wantForbidden(t, "SetMultifocus naming a window that does not exist", resp)
}
