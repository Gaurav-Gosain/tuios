//go:build linux || darwin

package session

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
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
// the process on its own socket by pid, and is answered as a connection from
// that process would be. A link may not ask.
func TestPaneGrantsAnswersForAPeerPID(t *testing.T) {
	d, sp, a1, _, _ := scopeFixture(t)
	setStrict(d, "read")
	const inPane = 4242
	d.setApprovalPeer(func(cs *connState) (bool, string) {
		if cs.peerPID == inPane {
			return true, a1
		}
		return false, ""
	})
	c := dialVerb(t, sp)
	got := result(t, callP(c, t, "pane-grants", map[string]any{"peer_pid": inPane}))
	if got["pane"] != true || got["window"] != a1 || !jsonEqual(got["grants"], []any{"read"}) {
		t.Errorf("pane-grants for a pid in pane a1 = %v, want pane a1 holding read", got)
	}
	// A live process outside every pane: the test runner that started this
	// binary.
	if got := result(t, callP(c, t, "pane-grants", map[string]any{"peer_pid": os.Getppid()})); got["pane"] != false {
		t.Errorf("pane-grants for a pid outside every pane = %v, want pane false", got)
	}
	// A pid no process has is held, as a caller that cannot be read is.
	if got := result(t, callP(c, t, "pane-grants", map[string]any{"peer_pid": deadPID(t)})); got["pane"] != true || got["window"] != unplacedWindow {
		t.Errorf("pane-grants for a gone pid = %v, want it held in no pane", got)
	}
	if code := errCode(t, callP(c, t, "pane-grants", map[string]any{"peer_pid": 0})); code != ErrVerbInvalidParams {
		t.Errorf("peer_pid 0 answered %s, want invalid_params", code)
	}
	wantForbidden(t, "peer_pid over a link", callP(dialLink(t, sp), t, "pane-grants", map[string]any{"peer_pid": inPane}))
}
