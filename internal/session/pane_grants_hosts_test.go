//go:build !slim && (linux || darwin)

package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

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

	if changes, _ := got["changes"].([]any); len(changes) != 1 || changes[0] != "Host build is added." {
		t.Errorf("apply-config with host reports %v, want only the added host", got["changes"])
	}

	got = result(t, callP(c, t, "apply-config", nil))
	changes := fmt.Sprint(got["changes"])
	if list, _ := got["changes"].([]any); len(list) != 1 {
		t.Errorf("apply-config reports %v, want only the mode change", got["changes"])
	}
	if got["mode"] != "open" || !strings.Contains(changes, "mode open") || !strings.Contains(changes, "Before: mode strict") {
		t.Errorf("apply-config did not say the mode changed: %v", got)
	}
}

// TestADroppedHostIsInTheInbox: a host whose ssh_options hold a refused
// option is dropped. The person sees it in the Inbox, with the reason, and
// the item closes when the entry is fixed.
//
// Negative control: with noteHostProblems cut from ApplyHosts, the Inbox has
// no item.
func TestADroppedHostIsInTheInbox(t *testing.T) {
	t.Setenv("TUIOS_SSH", "/bin/false")
	d, sp := startTestDaemon(t)
	d.ApplyHosts([]federation.Host{{Name: "work", Addr: "work.invalid", SSHOptions: []string{"-o", "UserKnownHostsFile=/tmp/x"}}})
	c := dialVerb(t, sp)
	find := func() string {
		items, _ := listAttention(t, c, `{}`)
		for _, it := range items {
			if it["name"] == hostProblemsNotice {
				return fmt.Sprint(it["summary"])
			}
		}
		return ""
	}
	if got := find(); !strings.Contains(got, "work") || !strings.Contains(got, "UserKnownHostsFile") || !strings.Contains(got, "config.toml") {
		t.Fatalf("the Inbox item about the dropped host = %q, want the host, the option and what to do", got)
	}
	d.ApplyHosts([]federation.Host{{Name: "work", Addr: "work.invalid"}})
	if got := find(); got != "" {
		t.Errorf("the item stayed after the entry was fixed: %q", got)
	}
}

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
