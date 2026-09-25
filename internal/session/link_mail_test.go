package session

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Mail from a machine whose link policy holds it. See hold_mail in
// internal/config's link_policy.go and verbReleaseAgentMessage.

func holdAll() map[string]config.HostConfig {
	hold := true
	return map[string]config.HostConfig{"*": {HoldMail: &hold}}
}

func TestOnlyThePersonReleasesHeldMail(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, _ := twoWindowSession(t, d, "work")
	d.SetLinkPolicies(holdAll())
	link := dialLink(t, sp)
	sent := result(t, sendJSON(t, link, 1, map[string]any{"session": "work", "to": a, "from": "planner", "from_host": "laptop", "text": "hello"}))
	id := sent["message_id"]

	local := dialVerb(t, sp)
	mustRefuse(t, callVerb(t, local, "release-agent-message", map[string]any{"session": "work", "id": id}),
		ErrVerbNotHuman, "a release with no nonce")
	// The link itself cannot release what its own policy held.
	mustRefuse(t, callVerb(t, link, "release-agent-message", map[string]any{"session": "work", "id": id, "human_nonce": "x"}),
		ErrVerbForbidden, "a release from the link the mail came on")

	tui := attachTUI(t, sp, "work")
	rel := result(t, callVerb(t, local, "release-agent-message", map[string]any{"session": "work", "id": id, "human_nonce": tui.HumanNonce()}))
	if rel["to"] != a {
		t.Fatalf("released to %v, want %s", rel["to"], a)
	}
	agentView := result(t, callVerb(t, local, "read-agent-messages", map[string]any{"session": "work", "to": a, "peek": true}))
	msgs, _ := agentView["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("the agent has %d messages after the release, want 1", len(msgs))
	}
	m := msgs[0].(map[string]any)
	if m["origin"] != AgentOriginLink || m["origin_host"] != "laptop" || m["from_label"] != "planner" || m["released_from"] != id {
		t.Errorf("the released message lost where it came from: %v", m)
	}
	for _, it := range attentionItems(t, local) {
		if it["kind"] == AttentionMail {
			t.Errorf("the held item is still open after the release: %v", it)
		}
	}
	mustRefuse(t, callVerb(t, local, "release-agent-message", map[string]any{"session": "work", "id": id, "human_nonce": tui.HumanNonce()}),
		ErrVerbInvalidParams, "a second release of the same message")
}

// attentionItems lists the Inbox.
func attentionItems(t *testing.T, c *verbConn) []map[string]any {
	t.Helper()
	res := result(t, callVerb(t, c, "list-attention", map[string]any{}))
	raw, _ := res["items"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, it := range raw {
		out = append(out, it.(map[string]any))
	}
	return out
}
