//go:build !slim

package session

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func TestStrictModeHoldsAPaneToItsGrants(t *testing.T) {
	d, sp, a1, a2, b1 := scopeFixture(t)
	setStrict(d)
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	c := dialVerb(t, sp)

	resp := callP(c, t, "list-sessions", nil)
	wantForbidden(t, "list-sessions", resp)
	hint := resp["error"].(map[string]any)["hint"].(map[string]any)
	if hint["verb"] != "pane-grants" || !strings.Contains(hint["detail"].(string), "[agents.permissions]") || !strings.Contains(hint["detail"].(string), "set-pane-grants") {
		t.Errorf("hint = %v, want one naming pane-grants, set-pane-grants and the config key", hint)
	}
	wantForbidden(t, "capture-pane of b", callP(c, t, "capture-pane", map[string]any{"session": "b", "window": b1}))
	wantForbidden(t, "send-text into b", callP(c, t, "send-text", map[string]any{"session": "b", "window": b1, "text": "x"}))
	wantForbidden(t, "new-window", callP(c, t, "new-window", map[string]any{"session": "a"}))
	wantForbidden(t, "kill-session", callP(c, t, "kill-session", map[string]any{"session": "b"}))
	wantForbidden(t, "set-option", callP(c, t, "set-option", map[string]any{"session": "a", "key": "x", "value": "y"}))

	// The default grants read, write and fan: its own session is open to it.
	listed := result(t, callP(c, t, "list-agents", nil))
	if listed["session"] != "a" {
		t.Errorf("list-agents with no session listed %v, want the pane's own a", listed["session"])
	}
	result(t, callP(c, t, "send-text", map[string]any{"window": a2, "text": "echo hi\r"}))
	sent := result(t, callP(c, t, "send-agent-message", map[string]any{"to": a2, "text": "hi"}))
	if sent["from"] != a1 {
		t.Errorf("mail from = %v, want the pane %s", sent["from"], a1)
	}
	result(t, callP(c, t, "set-agent-state", map[string]any{"state": "working"}))

	// respond needs its own grant, which strict does not give by default.
	wantForbidden(t, "respond", callP(c, t, "respond", map[string]any{"window": a2, "action": "approve"}))

	// The person, outside every pane, is untouched.
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	plain := dialVerb(t, sp)
	result(t, callP(plain, t, "list-sessions", nil))
	result(t, callP(plain, t, "capture-pane", map[string]any{"session": "b", "window": b1}))
	if got := result(t, callP(plain, t, "pane-grants", nil)); got["pane"] != false || got["mode"] != "strict" {
		t.Errorf("pane-grants from outside every pane = %v, want pane false under strict", got)
	}
}

func TestReadOnlyGrantRefusesTypingAndMail(t *testing.T) {
	d, sp, a1, a2, _ := scopeFixture(t)
	setStrict(d, "read")
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	c := dialVerb(t, sp)
	result(t, callP(c, t, "capture-pane", map[string]any{"window": a2}))
	wantForbidden(t, "send-text", callP(c, t, "send-text", map[string]any{"window": a2, "text": "x"}))
	wantForbidden(t, "send-keys", callP(c, t, "send-keys", map[string]any{"window": a2, "keys": "Enter"}))
	wantForbidden(t, "mail", callP(c, t, "send-agent-message", map[string]any{"to": a2, "text": "x"}))
	wantForbidden(t, "start-agent", callP(c, t, "start-agent", map[string]any{"agent": "true"}))
	// Its own record is always its own to write.
	result(t, callP(c, t, "set-agent-state", map[string]any{"state": "idle"}))
	result(t, callP(c, t, "set-agent-meta", map[string]any{"tokens": map[string]any{"model": "x"}}))
	wantForbidden(t, "another pane's record", callP(c, t, "set-agent-state", map[string]any{"window": a2, "state": "idle"}))

	// No grants at all still reports itself.
	d.manager.SetPanePermissions(config.ResolvedPermissions{Strict: true, Grants: []string{}})
	wantForbidden(t, "capture with no grants", callP(c, t, "capture-pane", map[string]any{"window": a2}))
	result(t, callP(c, t, "set-agent-state", map[string]any{"state": "working"}))
}

func TestRespondGrantLetsAPaneAnswer(t *testing.T) {
	d, sp, a1, a2, b1 := scopeFixture(t)
	setStrict(d, "read", "write", "respond")
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	if !d.paneMayRespond(&connState{}, "a") {
		t.Error("a pane holding respond may not answer in its own session")
	}
	if d.paneMayRespond(&connState{}, "b") {
		t.Error("a pane holding respond may answer in a session outside its reach")
	}
	c := dialVerb(t, sp)
	// Past the grant check, the handler looks for a prompt, and a2 shows
	// none: the refusal is the prompt's, not the caller's.
	resp := callP(c, t, "respond", map[string]any{"window": a2, "action": "approve"})
	if code := errCode(t, resp); code == ErrVerbForbidden || code == ErrVerbNotHuman {
		t.Errorf("a pane holding respond was refused as %s", code)
	}
	wantForbidden(t, "respond into b", callP(c, t, "respond", map[string]any{"session": "b", "window": b1, "action": "approve"}))

	// admin alone is not respond. An admin pane's call is not rewritten, so
	// it names its session the way any caller does.
	setStrict(d, "admin")
	resp = callP(c, t, "respond", map[string]any{"session": "a", "window": a2, "action": "approve"})
	if code := errCode(t, resp); code != ErrVerbNotHuman {
		t.Errorf("an admin pane without respond answered %s, want not_human as before grants", code)
	}
}

// TestAPaneCannotTypeIntoAPaneThatHoldsMore: the session check lets a pane
// type into its own session, but text typed into a sibling runs with the
// sibling's grants. A pane narrowed under open, next to shells on the open
// default (admin), must not be able to type set-pane-grants into one of them
// and widen itself.
func TestAPaneCannotTypeIntoAPaneThatHoldsMore(t *testing.T) {
	d, sp, a1, a2, _ := scopeFixture(t)
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	person := dialVerb(t, sp)
	result(t, callP(person, t, "set-pane-grants", map[string]any{"session": "a", "window": a1, "grants": []string{"read", "write"}}))

	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	c := dialVerb(t, sp)
	widen := "tuios set-pane-grants -w " + a1 + " --grants admin\r"
	resp := callP(c, t, "send-text", map[string]any{"window": a2, "text": widen})
	wantForbidden(t, "send-text into an admin sibling", resp)
	if msg := resp["error"].(map[string]any)["message"].(string); !strings.Contains(msg, "admin") || !strings.Contains(msg, "more than this pane holds") {
		t.Errorf("refusal %q does not say the target holds more", msg)
	}
	wantForbidden(t, "send-keys into an admin sibling", callP(c, t, "send-keys", map[string]any{"window": a2, "keys": "Enter"}))
	wantForbidden(t, "run in an admin sibling", callP(c, t, "run", map[string]any{"window": a2, "command": "true"}))
	wantForbidden(t, "ask-agent of an admin sibling", callP(c, t, "ask-agent", map[string]any{"window": a2, "text": "hi", "force": true}))

	// With no window the call means the focused pane, which is a2 here. It
	// is checked the same way.
	if f, _ := focusedWindowID(d.manager.GetSession("a").GetState()); f != a2 {
		t.Fatalf("focused window = %s, want %s for this check", f, a2)
	}
	wantForbidden(t, "send-text into the focused admin sibling", callP(c, t, "send-text", map[string]any{"text": widen}))

	// Its own pane is always its own to type into.
	result(t, callP(c, t, "send-text", map[string]any{"window": a1, "text": "x"}))

	// A sibling that holds no more than the caller may be typed into. The
	// person's connection is placed in no pane; c stays placed in a1.
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	result(t, callP(person, t, "set-pane-grants", map[string]any{"session": "a", "window": a2, "grants": []string{"read"}}))
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	result(t, callP(c, t, "send-text", map[string]any{"window": a2, "text": "x"}))
	result(t, callP(c, t, "send-keys", map[string]any{"window": a2, "keys": "Enter"}))
	// The window was pinned to the id it resolved to.
	out, verr := d.checkGrants(&connState{}, "send-text", json.RawMessage(`{"window":"Second","text":"x"}`))
	if verr != nil {
		t.Fatal(verr)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil || m["window"] != a2 {
		t.Errorf("params = %s, want window pinned to %s", out, a2)
	}
}
