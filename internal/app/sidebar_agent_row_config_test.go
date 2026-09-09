package app

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// agentRowSpec sets the rail's agent row table for a test and restores it.
func agentRowSpec(t *testing.T, m *OS, body string) {
	t.Helper()
	cfg, err := config.ParseUserConfig([]byte(body))
	if err != nil {
		t.Fatalf("ParseUserConfig: %v", err)
	}
	spec := config.ParseSidebarAgentRow(cfg.Appearance.Sidebar.AgentRow)
	if len(spec.Problems) != 0 {
		t.Fatalf("problems: %v", spec.Problems)
	}
	m.Settings.SidebarAgentRow = spec
}

// TestAgentRowDefaultDrawsTheRowItAlwaysDrew pins the shipped row: the
// session and harness prefix, the name, the elapsed figure at the right, and
// the harness and note on the second line.
func TestAgentRowDefaultDrawsTheRowItAlwaysDrew(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 40)
	lines, _ := m.sidebarPanelLinesForTree(tree)
	sep := sidebarAgentSep()
	row := stripANSIForTrace(railAgentRow(m, lines, "dddddddd4444"))
	if !strings.Contains(row, "api/server") {
		t.Fatalf("the foreign row lost its session prefix: %q", row)
	}
	if !strings.Contains(row, "codex"+sep+"awaiting app") {
		t.Fatalf("the note line lost the harness and message: %q", row)
	}
	local := stripANSIForTrace(railAgentRow(m, lines, "cccccccc3333"))
	if !strings.Contains(local, "build") || strings.Contains(local, "main/") {
		t.Fatalf("the local row = %q, want the bare name", local)
	}
	if !strings.Contains(local, "claude"+sep+"editing files") {
		t.Fatalf("the local row's note = %q, want the harness and message", local)
	}

	// Short rows: the harness rides the prefix when there is no note line.
	m.Height = 14
	lines, _ = m.sidebarPanelLinesForTree(tree)
	row = stripANSIForTrace(railAgentRow(m, lines, "dddddddd4444"))
	if !strings.Contains(row, "api/codex/server") {
		t.Fatalf("the short foreign row lost its prefix: %q", row)
	}
}

// TestAgentRowTokensReorderAndVanish: the list decides what a row carries and
// in what order, and a token with no value takes its separator with it.
func TestAgentRowTokensReorderAndVanish(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 40)
	agentRowSpec(t, m, `
[appearance.sidebar.agent_row]
tokens = ["name", "state", "host"]
`)
	lines, _ := m.sidebarPanelLinesForTree(tree)
	row := stripANSIForTrace(railAgentRow(m, lines, "dddddddd4444"))
	sep := sidebarAgentSep()
	if !strings.Contains(row, "server"+sep+"needs input") {
		t.Fatalf("row = %q, want the state after the name", row)
	}
	if strings.Contains(row, "api/") || strings.Contains(row, "codex") || strings.Contains(row, "awaiting") {
		t.Fatalf("row = %q still carries tokens the list left out", row)
	}
	if strings.Contains(row, "needs input"+sep) {
		t.Fatalf("row = %q carries a separator for the empty host token", row)
	}
	// With message out of the list no row is tall.
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowAgent && h.Y1-h.Y0 != 1 {
			t.Fatalf("an agent row is %d lines tall with no note token", h.Y1-h.Y0)
		}
	}
}

// TestAgentRowRuleInksOneToken: a rule on the name colours the name of the
// row whose name matches, and nothing else on the rail.
func TestAgentRowRuleInksOneToken(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 40)
	agentRowSpec(t, m, `
[[appearance.sidebar.agent_row.name.rule]]
contains = "serv"
fg = "#ff0000"
bold = true
`)
	lines, _ := m.sidebarPanelLinesForTree(tree)
	const red = "38;2;255;0;0"
	row := railAgentRow(m, lines, "dddddddd4444")
	if !strings.Contains(row, red) {
		t.Fatalf("the matching row's name is not red:\n%q", row)
	}
	if i := strings.Index(row, red); !strings.Contains(row[i:i+40], "server") {
		t.Fatalf("red ink landed away from the name:\n%q", row)
	}
	if strings.Contains(row[:strings.Index(row, red)], "api") && strings.Count(row, red) != 1 {
		t.Fatalf("the rule inked more than the name:\n%q", row)
	}
	other := railAgentRow(m, lines, "cccccccc3333")
	if strings.Contains(other, red) {
		t.Fatalf("a rule on one row inked another:\n%q", other)
	}
	for _, ln := range lines {
		if strings.Contains(stripANSIForTrace(ln), "sessions") && strings.Contains(ln, red) {
			t.Fatal("the rule reached the sessions header")
		}
	}
}

// TestAgentRowElapsedRuleReadsMinutes: gt on elapsed compares the minutes
// since the state was entered, not the "45m" text.
func TestAgentRowElapsedRuleReadsMinutes(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 40)
	agentRowSpec(t, m, `
[[appearance.sidebar.agent_row.elapsed.rule]]
gt = 30
fg = "#00ff00"
`)
	for i := range tree.Sessions[1].Children {
		if tree.Sessions[1].Children[i].ID == "dddddddd4444" {
			tree.Sessions[1].Children[i].StateAt = nowMinusMinutes(45)
		}
	}
	lines, _ := m.sidebarPanelLinesForTree(tree)
	row := railAgentRow(m, lines, "dddddddd4444")
	if !strings.Contains(stripANSIForTrace(row), "45m") {
		t.Fatalf("row = %q, want the 45m figure", stripANSIForTrace(row))
	}
	if !strings.Contains(row, "38;2;0;255;0") {
		t.Fatalf("the elapsed figure is not green after 45 minutes:\n%q", row)
	}
}

// nowMinusMinutes is a state stamp n minutes ago.
func nowMinusMinutes(n int) int64 {
	return time.Now().Add(-time.Duration(n) * time.Minute).UnixNano()
}
