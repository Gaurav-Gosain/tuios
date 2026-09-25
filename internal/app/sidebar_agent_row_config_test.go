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
