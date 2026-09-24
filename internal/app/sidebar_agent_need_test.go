package app

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// needTree is the attached session of sectionsTestOS plus one foreign session
// holding the given agent panes, which is where these tests put the rows they
// read.
func needTree(windows ...sessiontree.WindowInput) sessiontree.Tree {
	return sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, CurrentWorkspace: 1, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: "nvim", Focused: true, Workspace: 1},
		}},
		{Name: "api", CurrentWorkspace: 1, Windows: windows},
	})
}

// needOS is sectionsTestOS with only the plain shell left in the attached
// session, so every agent row on the rail is one the test put there.
func needOS(t *testing.T) *OS {
	t.Helper()
	m, _ := sectionsTestOS(t, 120, 40)
	m.Windows = m.Windows[:1]
	return m
}

// TestAgentRowNeedLiftsThePromptKind: a screen rule's message is "approval:
// <prompt>". The note says approval once, as the need, and keeps the prompt.
func TestAgentRowNeedLiftsThePromptKind(t *testing.T) {
	m := needOS(t)
	tree := needTree(sessiontree.WindowInput{
		ID: "dddddddd4444", Title: "server", AgentState: "needs_input", Harness: "codex",
		Message: "approval: ok?", StateAt: time.Now().Add(-7 * time.Minute).UnixNano(),
	})
	lines, _ := m.sidebarPanelLinesForTree(tree)
	row := stripANSIForTrace(railAgentRow(m, lines, "dddddddd4444"))
	sep := sidebarAgentSep()
	if !strings.Contains(row, "approval"+sep+"codex"+sep+"ok?") {
		t.Fatalf("row = %q, want the need, the harness and the prompt without its kind", row)
	}
	if strings.Contains(row, "approval: ") {
		t.Errorf("row = %q says approval twice", row)
	}
	// The full rail shows the wait at the right edge, so the need does not
	// repeat it.
	if !strings.Contains(row, "7m") || strings.Contains(row, "approval 7m") {
		t.Errorf("row = %q, want the wait once, at the right edge", row)
	}
}

// TestAgentRowNeedCarriesTheWaitWhenElapsedIsNotShown: how long a pane has
// waited on you is the figure a row that needs you must keep, so with no
// elapsed token on the identity line the need line carries it.
func TestAgentRowNeedCarriesTheWaitWhenElapsedIsNotShown(t *testing.T) {
	m := needOS(t)
	agentRowSpec(t, m, `
[appearance.sidebar.agent_row]
tokens = ["need", "name", "message"]
`)
	at := time.Now().Add(-12 * time.Minute).UnixNano()
	tree := needTree(
		sessiontree.WindowInput{ID: "dddddddd4444", Title: "server", AgentState: "needs_input", StateAt: at},
		sessiontree.WindowInput{ID: "eeeeeeee5555", Title: "tests", AgentState: "errored", Message: "exit 2", StateAt: at},
		sessiontree.WindowInput{ID: "ffffffff6666", Title: "docs", AgentState: "working", Message: "reading", StateAt: at},
	)
	lines, _ := m.sidebarPanelLinesForTree(tree)
	if row := stripANSIForTrace(railAgentRow(m, lines, "dddddddd4444")); !strings.Contains(row, "needs you 12m") {
		t.Errorf("blocked row = %q, want the need and the wait", row)
	}
	// A reported message stands in for the word, and the wait stays.
	if row := stripANSIForTrace(railAgentRow(m, lines, "eeeeeeee5555")); !strings.Contains(row, "waiting 12m"+sidebarAgentSep()+"exit 2") {
		t.Errorf("errored row = %q, want the wait and the message", row)
	}
	// A working row needs nothing and carries no wait.
	if row := stripANSIForTrace(railAgentRow(m, lines, "ffffffff6666")); strings.Contains(row, "12m") {
		t.Errorf("working row = %q carries a wait", row)
	}
}

// TestAgentRowNeedWords pins the word per state, which is what still says the
// state on a rail drawn with no colour.
func TestAgentRowNeedWords(t *testing.T) {
	for _, c := range []struct {
		state, message string
		seen           bool
		want           string
	}{
		{"needs_input", "", false, "needs you"},
		{"needs_input", "question: which branch?", false, "question"},
		{"needs_input", "approval: rm -rf build", false, "approval"},
		{"needs_input", "awaiting approval", false, ""},
		{"errored", "", false, "errored"},
		{"done", "", false, "done"},
		{"done", "", true, ""},
		{"working", "", false, ""},
		{"idle", "", false, ""},
	} {
		if got, _ := sidebarAgentNeed(c.state, c.seen, "", c.message); got != c.want {
			t.Errorf("need(%q, %q, seen=%v) = %q, want %q", c.state, c.message, c.seen, got, c.want)
		}
	}
}

// TestAgentRowDrawsMetadata: the meta token draws what the pane reported, in
// the pane's order, on the second line; a $key token places one key itself and
// meta then leaves that key out. The keys tuios feeds itself (model, context,
// cost, plan, now, prompt) have tokens of their own, and meta leaves them out
// too, so the model and cost are not on every row unless placed.
func TestAgentRowDrawsMetadata(t *testing.T) {
	m := needOS(t)
	meta := []sessiontree.MetaToken{{Key: "branch", Value: "main"}, {Key: "model", Value: "opus"}, {Key: "ticket", Value: "T-12"}, {Key: "context", Value: "42%"}}
	tree := needTree(sessiontree.WindowInput{
		ID: "dddddddd4444", Title: "server", AgentState: "working", Harness: "codex", Meta: meta,
	})
	lines, _ := m.sidebarPanelLinesForTree(tree)
	sep := sidebarAgentSep()
	row := stripANSIForTrace(railAgentRow(m, lines, "dddddddd4444"))
	if !strings.Contains(row, "codex"+sep+"main"+sep+"T-12") {
		t.Fatalf("row = %q, want the harness then the metadata in the pane's order", row)
	}
	if strings.Contains(row, "opus") || strings.Contains(row, "42%") {
		t.Fatalf("row = %q, want the fed model and a context under 80%% left off the shipped row", row)
	}

	agentRowSpec(t, m, `
[appearance.sidebar.agent_row]
tokens = ["name", "$context", "$model", "meta"]

[appearance.sidebar.agent_row."$context"]
fg = "warning"
`)
	lines, _ = m.sidebarPanelLinesForTree(tree)
	row = stripANSIForTrace(railAgentRow(m, lines, "dddddddd4444"))
	if !strings.Contains(row, "42%"+sep+"opus"+sep+"main") || strings.Count(row, "42%") != 1 {
		t.Fatalf("row = %q, want $context and $model placed first and meta without them", row)
	}
	styled := railAgentRow(m, lines, "dddddddd4444")
	if !strings.Contains(styled, fgParams(theme.UI().Warning)) {
		t.Errorf("the $context style did not reach the token: %q", styled)
	}
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

// TestRailStatesAreNotColourAlone: every state a row can be in must read
// without colour, through its glyph or its need word. Unread and read done
// used to differ only in ink.
func TestRailStatesAreNotColourAlone(t *testing.T) {
	s := config.Global
	s.SidebarShowGlyphs = true
	pal := overlay.Palette{}
	type look struct{ glyph, word string }
	seen := map[look]string{}
	for _, c := range []struct {
		name  string
		state string
		read  bool
	}{
		{"working", "working", false},
		{"needs_input", "needs_input", false},
		{"errored", "errored", false},
		{"finished unread", "done", false},
		{"idle", "idle", false},
		{"unknown", "unknown", false},
	} {
		l := look{
			glyph: stripANSIForTrace(sidebarGlyph(c.state, c.read, nil, pal, &s)),
			word:  func() string { w, _ := sidebarAgentNeed(c.state, c.read, "", ""); return w }(),
		}
		if other, dup := seen[l]; dup {
			t.Errorf("%s and %s look the same without colour: %+v", c.name, other, l)
		}
		seen[l] = c.name
	}
	// A finished pane already looked at is at rest, and draws as idle does.
	if got, want := stripANSIForTrace(sidebarGlyph("done", true, nil, pal, &s)), agentStateIndicator("idle"); got != want {
		t.Errorf("a read finished pane draws %q, want idle's %q", got, want)
	}
	if got, want := stripANSIForTrace(sidebarGlyph("done", false, nil, pal, &s)), agentStateIndicator("done"); got != want {
		t.Errorf("an unread finished pane draws %q, want %q", got, want)
	}
}
