package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// TestAgentMetaFromWireReusesAnUnchangedList: a state sync that moves nothing
// on the rail must not allocate for it.
func TestAgentMetaFromWireReusesAnUnchangedList(t *testing.T) {
	wire := []session.AgentMetaToken{{Key: "model", Value: "opus", Expires: 5}}
	first := agentMetaFromWire(nil, wire)
	if len(first) != 1 || first[0] != (sessiontree.MetaToken{Key: "model", Value: "opus"}) {
		t.Fatalf("converted = %v", first)
	}
	if again := agentMetaFromWire(first, wire); &again[0] != &first[0] {
		t.Error("an unchanged list was copied")
	}
	if changed := agentMetaFromWire(first, []session.AgentMetaToken{{Key: "model", Value: "sonnet"}}); changed[0].Value != "sonnet" || first[0].Value != "opus" {
		t.Error("a changed list was not copied, or the old one was written into")
	}
	if agentMetaFromWire(first, nil) != nil {
		t.Error("an empty wire list did not clear the pane's metadata")
	}
}

// TestMetaKeyRulesAgreeWithTheRail: the rail's config reads "$key" with its own
// copy of the key rules (config cannot import session). A key the daemon takes
// that the rail refuses could be stored and never placed.
func TestMetaKeyRulesAgreeWithTheRail(t *testing.T) {
	for _, k := range []string{"", "a", "model", "ctx_used", "cost-usd", "a1", "1a", "_a", "-a", "A", "mod el", "é",
		strings.Repeat("a", session.AgentMetaMaxKey), strings.Repeat("a", session.AgentMetaMaxKey+1)} {
		_, rail := config.SidebarMetaTokenKey("$" + k)
		if daemon := session.ValidAgentMetaKey(k); daemon != rail {
			t.Errorf("key %q: daemon %v, rail %v", k, daemon, rail)
		}
	}
}
