package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
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

// needOS is sectionsTestOS with only the plain shell left in the attached
// session, so every agent row on the rail is one the test put there.
func needOS(t *testing.T) *OS {
	t.Helper()
	m, _ := sectionsTestOS(t, 120, 40)
	m.Windows = m.Windows[:1]
	return m
}

// TestAgentRowMetaIsInTheSignature: a statusline tick changes nothing but the
// metadata, and a cache that cannot see it serves the old figure forever.
func TestAgentRowMetaIsInTheSignature(t *testing.T) {
	m := needOS(t)
	m.Windows = append(m.Windows, &terminal.Window{ID: "w-agent", CustomName: "agent", AgentState: "working"})
	base := m.sidebarSignature()
	m.Windows[1].AgentMeta = []sessiontree.MetaToken{{Key: "context", Value: "10%"}}
	withMeta := m.sidebarSignature()
	if withMeta == base {
		t.Fatal("agent metadata is not in the rail signature")
	}
	m.Windows[1].AgentMeta = []sessiontree.MetaToken{{Key: "context", Value: "11%"}}
	if m.sidebarSignature() == withMeta {
		t.Error("a changed metadata value is not in the rail signature")
	}
}
