//go:build linux || darwin

package session

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// These respond tests run a helper process in a pane and read its peer pid
// off the socket, which the helpers in human_origin_test.go do on linux and
// darwin only.

// TestRespondFromShellIsAGrant: without the grant, a caller outside every pane
// with no attach is refused; with it, the same caller may answer. A call on
// the plain link socket, whose hub did not vouch for its caller, is refused
// either way.
func TestRespondFromShellIsAGrant(t *testing.T) {
	skipWithoutPeerPID(t)
	d, sp := startTestDaemon(t)
	ag := startBlockedAgent(t, d, sp, "grant", "claude-code", "approve")
	params := map[string]any{"session": "grant", "window": ag.window, "action": "approve"}

	c := dialVerb(t, sp)
	if code := errCode(t, callVerb(t, c, "respond", params)); code != ErrVerbNotHuman {
		t.Fatalf("without the grant: %s, want %s", code, ErrVerbNotHuman)
	}
	d.respondFromShell = true
	link := dialLink(t, sp)
	// A link may not respond at all under the default policy.
	if code := errCode(t, callVerb(t, link, "respond", params)); code != ErrVerbForbidden {
		t.Fatalf("on the plain link socket with the default link policy: %s, want %s", code, ErrVerbForbidden)
	}
	// With respond allowed, the plain link socket still carries no one the
	// grant covers.
	d.SetLinkPolicies(map[string]config.HostConfig{"*": {Allow: config.LinkCapabilities}})
	if code := errCode(t, callVerb(t, link, "respond", params)); code != ErrVerbNotHuman {
		t.Fatalf("on the plain link socket with the grant: %s, want %s", code, ErrVerbNotHuman)
	}
	res := result(t, callVerb(t, c, "respond", params))
	if res["sent"] != "1" {
		t.Errorf("with the grant: %v", res)
	}
}

// TestAPaneCannotRespondWithACopiedNonce holds respond to the rule every act
// as the person is held to: an agent in a pane that got the person's live
// nonce still cannot approve a prompt, its own or another pane's, and the
// grant does not change that.
func TestAPaneCannotRespondWithACopiedNonce(t *testing.T) {
	skipWithoutPeerPID(t)
	d, sp := startTestDaemon(t)
	ag := startBlockedAgent(t, d, sp, "paneh", "claude-code", "approve")
	d.respondFromShell = true
	tui := attachTUI(t, sp, "paneh")
	out := filepath.Join(t.TempDir(), "out")
	req := `{"id":1,"verb":"respond","params":{"session":"paneh","window":"` + ag.window + `","action":"approve","human_nonce":"` + tui.HumanNonce() + `"}}`
	runInPane(t, d, ag.sess, ag.other, helperCommand(t, sp, out, "send", req))
	var resp map[string]any
	if err := json.Unmarshal([]byte(waitHelper(t, out)), &resp); err != nil {
		t.Fatal(err)
	}
	if code := errCode(t, resp); code != ErrVerbNotHuman {
		t.Fatalf("respond from a pane with the person's nonce: %s, want %s", code, ErrVerbNotHuman)
	}
	if got := ag.received(); got != "" {
		t.Fatalf("ASSERTION: a pane's respond reached the agent: %q", got)
	}
}

// TestRespondOverTheLink answers a prompt on another machine: the person's
// client attached to the far session through the hub, whose nonce the far
// daemon issued, answers over a verb stream through the same hub. A nonce is
// the far daemon's to check, and the hub vouches for the stream as it does for
// a reply from human.
func TestRespondOverTheLink(t *testing.T) {
	skipWithoutPeerPID(t)
	hub, far := startHubAndLinkedFar(t)
	ag := startBlockedAgent(t, far.daemon, far.socket, "far", "claude-code", "approve")
	waitForHostUp(t, hub, "build")

	vc, _, err := DialVerbClientThroughHost("build", "test")
	if err != nil {
		t.Fatalf("dial through host: %v", err)
	}
	t.Cleanup(func() { _ = vc.Close() })
	raw, err := vc.Call("peek-prompt", map[string]any{"session": "far", "window": ag.window})
	if err != nil {
		t.Fatalf("peek over the link: %v", err)
	}
	var pk map[string]any
	_ = json.Unmarshal(raw, &pk)
	if pk["answerable"] != true {
		t.Fatalf("peek over the link: %v", pk)
	}

	params := map[string]any{"session": "far", "window": ag.window, "action": "approve", "prompt_id": pk["prompt_id"]}
	// Answering for the person is not in the default link policy.
	if _, err := vc.Call("respond", params); err == nil || !strings.Contains(err.Error(), ErrVerbForbidden) {
		t.Fatalf("respond over a link with the default policy: %v, want %s", err, ErrVerbForbidden)
	}
	far.daemon.SetLinkPolicies(map[string]config.HostConfig{"*": {Allow: config.LinkCapabilities}})
	if _, err := vc.Call("respond", params); err == nil || !strings.Contains(err.Error(), ErrVerbNotHuman) {
		t.Fatalf("respond over the link with no nonce: %v, want %s", err, ErrVerbNotHuman)
	}
	person, _ := connectThrough(t, "build", "far")
	params["human_nonce"] = person.HumanNonce()
	raw, err = vc.Call("respond", params)
	if err != nil {
		t.Fatalf("respond over the link: %v", err)
	}
	var res map[string]any
	_ = json.Unmarshal(raw, &res)
	if res["sent"] != "1" {
		t.Errorf("respond over the link: %v", res)
	}
	if got := ag.received(); got != "1" {
		t.Errorf("the far agent read %q, want 1", got)
	}
}
