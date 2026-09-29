//go:build linux || darwin

package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestAPaneCannotDialTheLinkSocket: the link socket is for the link proxy,
// which runs outside every pane. A process in a pane that dials it itself
// would be held to the link policy and not to its pane's grants, so it is
// refused whatever it asks. The same call from outside every pane, this test
// process, is still served as a link.
//
// Negative control: with the linkFromPane check cut from checkLinkVerb, the
// helper's send-text is served.
func TestAPaneCannotDialTheLinkSocket(t *testing.T) {
	skipWithoutPeerPID(t)
	d, sp := startTestDaemon(t)
	setStrict(d, "read")
	sess, a, b := twoWindowSession(t, d, "linkpane")

	req := `{"id":1,"verb":"send-text","params":{"session":"linkpane","window":"` + a + `","text":"x"}}`
	for _, sock := range []string{LinkSocketPath(sp), LinkHumanSocketPath(sp)} {
		out := filepath.Join(t.TempDir(), "out")
		runInPane(t, d, sess, b, helperCommand(t, sock, out, "send", req))
		raw := waitHelper(t, out)
		var resp map[string]any
		if err := json.Unmarshal([]byte(raw), &resp); err != nil {
			t.Fatalf("helper on %s said %q", filepath.Base(sock), raw)
		}
		e, _ := resp["error"].(map[string]any)
		if e == nil || e["code"] != ErrVerbForbidden {
			t.Fatalf("a pane holding read typed through %s: %v", filepath.Base(sock), resp)
		}
		if msg, _ := e["message"].(string); !strings.Contains(msg, "link") {
			t.Errorf("refusal %q does not say it is about the link socket", msg)
		}
	}

	// The proxy runs outside every pane, as this process does.
	link := dialLink(t, sp)
	result(t, link.call(t, req))
}

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

// TestAnAdminPaneCannotAnswerAnotherPanesPrompt: under the default open mode
// every pane holds admin. Keys typed into a pane waiting on a prompt answer
// it, so typing into another pane on needs_input takes the respond grant even
// for admin. The person, and the pane itself, are not held by it.
//
// Negative control: with the admin early return back in front of the prompt
// check in checkGrants, the send-keys below is served.
func TestAnAdminPaneCannotAnswerAnotherPanesPrompt(t *testing.T) {
	d, sp, a1, a2, _ := scopeFixture(t)
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	person := dialVerb(t, sp)
	setAgentState(t, person, "a", a2, string(AgentStateNeedsInput), "approval", "run rm -rf build?")

	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	c := dialVerb(t, sp)
	for _, call := range []struct {
		verb   string
		params map[string]any
	}{
		{"send-keys", map[string]any{"session": "a", "window": a2, "keys": "1 Enter"}},
		{"send-text", map[string]any{"session": "a", "window": a2, "text": "1\r"}},
		{"ask-agent", map[string]any{"session": "a", "window": a2, "text": "1", "allow_blocked": true, "force": true}},
		{"run", map[string]any{"session": "a", "window": a2, "command": "true"}},
	} {
		resp := callP(c, t, call.verb, call.params)
		wantForbidden(t, call.verb+" from an admin pane into a prompt", resp)
		if e, _ := resp["error"].(map[string]any); e != nil {
			if msg, _ := e["message"].(string); !strings.Contains(msg, "respond grant") {
				t.Errorf("%s refusal %q does not name the respond grant", call.verb, msg)
			}
		}
	}
	// The focused pane, reached with no window, is held the same way.
	if err := d.manager.GetSession("a").mutateState(func(st *SessionState) error { st.FocusedWindowID = a2; return nil }); err != nil {
		t.Fatal(err)
	}
	wantForbidden(t, "send-keys with no window into a focused prompt", callP(c, t, "send-keys", map[string]any{"session": "a", "keys": "1 Enter"}))

	// Its own pane is its own.
	result(t, callP(c, t, "send-text", map[string]any{"session": "a", "window": a1, "text": "x"}))

	// The person gives respond: then it may.
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	result(t, callP(person, t, "set-pane-grants", map[string]any{"session": "a", "window": a1, "grants": []string{"admin", "respond"}}))
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	result(t, callP(dialVerb(t, sp), t, "send-text", map[string]any{"session": "a", "window": a2, "text": "1\r"}))

	// The person is never held by it.
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	result(t, callP(dialVerb(t, sp), t, "send-text", map[string]any{"session": "a", "window": a2, "text": "1\r"}))
}

// TestPaneGrantsAnswersForAPeerPID: the tmux shim's pane holder asks about
// the process on its own socket by pid and start time, and is answered as a
// connection from that process would be. The answer echoes peer_pid, which a
// daemon that ignores the param would not. A pane without admin learns only
// pane and admin about a process in another pane. A link may not ask.
func TestPaneGrantsAnswersForAPeerPID(t *testing.T) {
	skipWithoutPeerPID(t)
	d, sp, a1, a2, _ := scopeFixture(t)
	setStrict(d, "read")
	inPane := os.Getppid()
	start, _ := procinfo.StartTime(inPane)
	var callerWindow string
	d.setApprovalPeer(func(cs *connState) (bool, string) {
		switch {
		case cs.peerPID == inPane:
			return true, a1
		case cs.peerPID == os.Getpid() && callerWindow != "":
			return true, callerWindow
		}
		return false, ""
	})
	c := dialVerb(t, sp)
	got := result(t, callP(c, t, "pane-grants", map[string]any{"peer_pid": inPane, "peer_start": start}))
	if got["pane"] != true || got["window"] != a1 || !jsonEqual(got["grants"], []any{"read"}) || got["peer_pid"] != float64(inPane) {
		t.Errorf("pane-grants for a pid in pane a1 = %v, want pane a1 holding read, peer_pid echoed", got)
	}
	// Another process that holds the pid now is not the one that connected.
	if got := result(t, callP(c, t, "pane-grants", map[string]any{"peer_pid": inPane, "peer_start": start + 1})); got["window"] != unplacedWindow {
		t.Errorf("pane-grants for a pid with another start time = %v, want it held in no pane", got)
	}
	// pid 1 runs outside every pane.
	if got := result(t, callP(c, t, "pane-grants", map[string]any{"peer_pid": 1})); got["pane"] != false {
		t.Errorf("pane-grants for a pid outside every pane = %v, want pane false", got)
	}
	if got := result(t, callP(c, t, "pane-grants", map[string]any{"peer_pid": deadPID(t)})); got["pane"] != true || got["window"] != unplacedWindow {
		t.Errorf("pane-grants for a gone pid = %v, want it held in no pane", got)
	}
	if code := errCode(t, callP(c, t, "pane-grants", map[string]any{"peer_pid": 0})); code != ErrVerbInvalidParams {
		t.Errorf("peer_pid 0 answered %s, want invalid_params", code)
	}
	wantForbidden(t, "peer_pid over a link", callP(dialLink(t, sp), t, "pane-grants", map[string]any{"peer_pid": inPane}))

	// A pane without admin, asking about a process in another pane, learns
	// only pane and admin.
	callerWindow = a2
	other := dialVerb(t, sp)
	got = result(t, callP(other, t, "pane-grants", map[string]any{"peer_pid": inPane, "peer_start": start}))
	if got["pane"] != true || got["admin"] != false || got["window"] != nil || got["grants"] != nil {
		t.Errorf("pane-grants from another pane = %v, want only pane and admin", got)
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

// TestAHostsReloadOnlyNarrows: ssh runs what a host entry says as the
// daemon's child, and a process in a pane can write config.toml. So a reload
// drops a host at once, but a new host, or one that dials another way, waits
// for the person's apply-config. So does a link policy that gives a machine
// more; one that gives less applies.
//
// Negative control: with onConfigReload applying [hosts] as read, the new
// host is in the table after the reload.
func TestAHostsReloadOnlyNarrows(t *testing.T) {
	t.Setenv("TUIOS_SSH", "/bin/false")
	d, sp := startTestDaemon(t)
	d.configPath = filepath.Join(t.TempDir(), "config.toml")

	body := "[hosts.build]\naddr = \"build.invalid\"\n\n[hosts.\"*\"]\nallow = [\"list\", \"respond\"]\n"
	if err := os.WriteFile(d.configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseUserConfig([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	d.onConfigReload(cfg, nil)
	if _, err := d.federation.Table().Lookup("build"); err == nil {
		t.Fatal("a host added to config.toml was dialled without the person")
	}
	if !d.configWaiting() {
		t.Error("the daemon does not say a change waits")
	}
	pol := d.linkPolicy(&connState{})
	if pol.Allows(config.LinkAllowRespond) || pol.Allows(config.LinkAllowWrite) {
		t.Errorf("the link policy after the reload allows %v, want list only: respond waits and write was taken away", pol.Allow)
	}
	if !pol.Allows(config.LinkAllowList) {
		t.Errorf("the link policy after the reload allows %v, want list", pol.Allow)
	}

	// The person applies it.
	c := dialVerb(t, sp)
	result(t, callP(c, t, "apply-config", nil))
	if _, err := d.federation.Table().Lookup("build"); err != nil {
		t.Errorf("apply-config did not add the host: %v", err)
	}
	if !d.linkPolicy(&connState{}).Allows(config.LinkAllowRespond) {
		t.Error("apply-config did not apply the link policy")
	}
	if d.configWaiting() {
		t.Error("a change still waits after apply-config")
	}

	// A host that is gone goes at once.
	d.onConfigReload(&config.UserConfig{}, nil)
	if _, err := d.federation.Table().Lookup("build"); err == nil {
		t.Error("a host removed from config.toml is still dialled")
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

// TestAStartSaysWhenGrantsWidened: a pane can widen config.toml and end the
// daemon, so the next start reads the wider file. The start cannot tell who
// started it, so it says so: in pane-grants, and in the Inbox.
//
// Negative control: with checkGrantsSinceLastRun cut from Start, the second
// daemon opens no item and pane-grants says nothing.
func TestAStartSaysWhenGrantsWidened(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", testutil.RuntimeDir(t))
	t.Cleanup(useResurrectionDir(t.TempDir()))
	start := func(perms config.ResolvedPermissions) (*Daemon, string) {
		d := NewDaemon(&DaemonConfig{Version: "test", DisableAutoRestore: true, Permissions: perms})
		if err := d.Start(); err != nil {
			t.Fatalf("daemon Start: %v", err)
		}
		sp, err := GetSocketPath()
		if err != nil {
			t.Fatal(err)
		}
		return d, sp
	}
	d1, sp := start(config.ResolvedPermissions{Strict: true, Grants: []string{"read"}})
	if got := result(t, callP(dialVerb(t, sp), t, "pane-grants", nil)); got["widened_at_start"] != nil {
		t.Errorf("the first start says grants widened: %v", got)
	}
	d1.Stop()

	d2, sp := start(config.ResolvedPermissions{})
	t.Cleanup(d2.Stop)
	c := dialVerb(t, sp)
	if got := result(t, callP(c, t, "pane-grants", nil)); got["widened_at_start"] != true {
		t.Errorf("a start with wider grants does not say so: %v", got)
	}
	items, _ := listAttention(t, c, `{}`)
	found := false
	for _, it := range items {
		if strings.Contains(fmt.Sprint(it["summary"]), "hold more than when tuios last ran") {
			found = true
		}
	}
	if !found {
		t.Errorf("the Inbox has no item about the wider grants: %v", items)
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

// TestAnAdminPaneCannotTypeIntoAPromptOverTheClientProtocol: the client
// protocol's input message writes into any pane of the attached session. From
// a pane that holds admin, which every pane holds under open, it is held to
// the prompt rule like send-text. The person's input is not.
//
// Negative control: with refuseTypingInto cut from handleInput, the pane's
// input reaches the pane on the prompt.
func TestAnAdminPaneCannotTypeIntoAPromptOverTheClientProtocol(t *testing.T) {
	d, sp, a1, a2, _ := scopeFixture(t)
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	person := dialVerb(t, sp)
	setAgentState(t, person, "a", a2, string(AgentStateNeedsInput), "approval", "approve Bash: rm -rf build")
	sess := d.manager.GetSession("a")
	st := sess.GetState()
	var ptyID string
	for _, w := range st.Windows {
		if w.ID == a2 {
			ptyID = w.PTYID
		}
	}
	input := func(placed bool, text string) {
		t.Helper()
		d.setApprovalPeer(func(*connState) (bool, string) {
			if placed {
				return true, a1
			}
			return false, ""
		})
		server, client := net.Pipe()
		t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
		go func() { _, _ = io.Copy(io.Discard, client) }()
		cs := &connState{conn: server, clientID: "input-" + text, sessionID: sess.ID}
		var buf bytes.Buffer
		if err := WritePTYInput(&buf, ptyID, []byte(text)); err != nil {
			t.Fatal(err)
		}
		msg, err := ReadMessage(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.handleInput(cs, msg); err != nil {
			t.Fatal(err)
		}
	}
	input(true, "echo PANE_TYPED_IT\r")
	if waitPaneText(person, t, "a", a2, "PANE_TYPED_IT") {
		t.Fatal("an admin pane typed into a pane on a prompt over the client protocol")
	}
	input(false, "echo PERSON_TYPED_IT\r")
	if !waitPaneText(person, t, "a", a2, "PERSON_TYPED_IT") {
		t.Fatal("the person's input did not reach the pane")
	}
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

// TestApplyingOneHostAppliesNothingElse: tuios hosts add applies its own
// host. A widening another process wrote to config.toml, such as mode open,
// is not applied with it. tuios config apply applies it and says so.
//
// Negative control: with apply-config ignoring host, the first call applies
// mode open and the pane holds admin.
func TestApplyingOneHostAppliesNothingElse(t *testing.T) {
	t.Setenv("TUIOS_SSH", "/bin/false")
	d, sp, a1, _, _ := scopeFixture(t)
	setStrict(d, "read")
	d.configPath = filepath.Join(t.TempDir(), "config.toml")
	body := "[agents.permissions]\nmode = \"open\"\n\n[hosts.build]\naddr = \"build.invalid\"\n"
	if err := os.WriteFile(d.configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c := dialVerb(t, sp)
	got := result(t, callP(c, t, "apply-config", map[string]any{"host": "build"}))
	if _, err := d.federation.Table().Lookup("build"); err != nil {
		t.Errorf("apply-config with host did not add the host: %v", err)
	}
	if g, _ := d.manager.grants.effective(a1); g != GrantRead {
		t.Fatalf("apply-config with host applied the permissions too: the pane holds %v", g)
	}
	if got["mode"] != "strict" {
		t.Errorf("apply-config with host reports mode %v, want strict", got["mode"])
	}

	got = result(t, callP(c, t, "apply-config", nil))
	changes := fmt.Sprint(got["changes"])
	if got["mode"] != "open" || !strings.Contains(changes, "mode open") || !strings.Contains(changes, "Before: mode strict") {
		t.Errorf("apply-config did not say the mode changed: %v", got)
	}
}
