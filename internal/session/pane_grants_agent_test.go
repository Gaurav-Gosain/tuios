//go:build !slim

package session

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func TestStrictStreamCarriesOnlyReadableSessions(t *testing.T) {
	d, sp, a1, _, b1 := scopeFixture(t)
	setStrict(d)
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	c := dialVerb(t, sp)
	ack := result(t, callP(c, t, "subscribe", map[string]any{"types": []string{EventAgentState}}))
	if ack["type"] != EventSubscribed {
		t.Fatalf("subscribe ack = %v", ack)
	}
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	plain := dialVerb(t, sp)
	setAgentState(t, plain, "b", b1, "working", "", "")
	setAgentState(t, plain, "a", a1, "working", "", "")
	if ev := readEvent(t, c); ev["session"] != "a" {
		t.Fatalf("the first event on a strict pane's stream is %v, want a's; b's must not be written", ev)
	}
}

// TestTypingIntoAPromptNeedsRespond: keys typed into a pane waiting on a
// prompt answer it, which is what the respond grant is for. write alone, the
// default under strict, does not answer a sibling's approval menu.
func TestTypingIntoAPromptNeedsRespond(t *testing.T) {
	d, sp, a1, a2, _ := scopeFixture(t)
	setStrict(d)
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	person := dialVerb(t, sp)
	setAgentState(t, person, "a", a2, string(AgentStateNeedsInput), "approval", "run rm -rf build?")

	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	c := dialVerb(t, sp)
	resp := callP(c, t, "send-keys", map[string]any{"window": a2, "keys": "Enter"})
	wantForbidden(t, "send-keys into a prompt", resp)
	if msg := resp["error"].(map[string]any)["message"].(string); !strings.Contains(msg, "respond grant") {
		t.Errorf("refusal %q does not name the respond grant", msg)
	}
	wantForbidden(t, "send-text into a prompt", callP(c, t, "send-text", map[string]any{"window": a2, "text": "y\r"}))
	wantForbidden(t, "ask-agent allow_blocked into a prompt", callP(c, t, "ask-agent", map[string]any{"window": a2, "text": "y", "allow_blocked": true, "force": true}))
	// Without allow_blocked ask-agent keeps its own refusal.
	if code := errCode(t, callP(c, t, "ask-agent", map[string]any{"window": a2, "text": "y"})); code != ErrVerbAgentBlocked {
		t.Errorf("ask-agent into a prompt answered %s, want agent_blocked as before", code)
	}

	// respond covers it.
	setStrict(d, append(append([]string{}, config.DefaultStrictGrants...), config.PaneGrantRespond)...)
	result(t, callP(c, t, "send-keys", map[string]any{"window": a2, "keys": "Enter"}))

	// A pane that has come to a prompt since the call was checked is refused
	// by the handler's second look.
	setStrict(d)
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	setAgentState(t, person, "a", a2, string(AgentStateIdle), "", "")
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	cs := &connState{}
	if _, verr := d.checkGrants(cs, "send-text", json.RawMessage(`{"window":"`+a2+`","text":"x"}`)); verr != nil {
		t.Fatal(verr)
	}
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	setAgentState(t, person, "a", a2, string(AgentStateNeedsInput), "approval", "again?")
	if verr := d.recheckTyping(cs, "send-text", d.manager.GetSession("a"), a2); verr == nil || verr.Code != ErrVerbForbidden {
		t.Errorf("recheck of a pane now on a prompt = %v, want forbidden", verr)
	}
	// The person is never held by it.
	if verr := d.recheckTyping(&connState{}, "send-text", d.manager.GetSession("a"), a2); verr != nil {
		t.Errorf("recheck for a connection that ran no checked call = %v", verr)
	}
}

func TestPaneGrantsSurviveClientSyncAndRestore(t *testing.T) {
	d, sp, a1, _, _ := scopeFixture(t)
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	c := dialVerb(t, sp)
	result(t, callP(c, t, "set-pane-grants", map[string]any{"session": "a", "window": a1, "grants": []string{"read"}}))

	sess := d.manager.GetSession("a")
	push := sess.GetState()
	for i := range push.Windows {
		push.Windows[i].Grants = []string{"admin"}
	}
	sess.UpdateState(push)
	if got := sess.GetState().Windows[0].Grants; !slices.Equal(got, []string{"read"}) {
		t.Errorf("after a client push the grants read %v, want [read]: a client cannot set them", got)
	}

	saved := savedAgentSession("regranted")
	saved.Windows[3].Grants = []string{"read", "unknown-later"}
	saved.Windows[4].Grants = []string{"none"}
	restored, err := d.restoreSession(saved)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	for _, w := range restored.GetState().Windows {
		g, explicit := d.manager.grants.effective(w.ID)
		switch w.ID {
		case "win-plain":
			if g != GrantRead || !explicit || !slices.Equal(w.Grants, []string{"read"}) {
				t.Errorf("win-plain holds %v (explicit %v, recorded %v), want read", g, explicit, w.Grants)
			}
		case "win-ended":
			if g != 0 || !explicit || !slices.Equal(w.Grants, []string{"none"}) {
				t.Errorf("win-ended holds %v (explicit %v, recorded %v), want nothing", g, explicit, w.Grants)
			}
		case "win-agent":
			if explicit || w.Grants != nil {
				t.Errorf("win-agent holds %v explicit, want the default", g)
			}
		}
	}
}
