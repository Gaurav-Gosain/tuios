package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// TestNarrowNoteKeepsTheSubagentCount: a value on the note line is kept whole
// or dropped from the end, so a row at rest reading "claude · ctx 91% · 3
// subagents" lost the count first on a narrow rail, and the count is the one
// sign on that row that work goes on. The harness gives way to it: at every
// width that holds the warning and the count, the count is drawn, and a width
// with room for all three keeps the harness as well. A row with no subagents
// says nothing of them, which is the positive half.
func TestNarrowNoteKeepsTheSubagentCount(t *testing.T) {
	m, _ := sectionsTestOS(t, 120, 30)
	pal := theme.UI()
	e := sidebarAgentEntry{
		Title: "api", State: "done", DoneSeen: true, Harness: "claude-code",
		Meta:      []sessiontree.MetaToken{{Key: "context", Value: "91%"}},
		Subagents: 3,
	}
	row := func(e sidebarAgentEntry, cw int) string {
		return ansi.Strip(m.sidebarAgentNoteRow(e, sidebarVariantFull, cw, pal, sidebarRowState{}))
	}
	// The line has cw-4 cells, and "ctx 91% · 3 subagents" takes 21.
	for cw := 25; cw <= 40; cw++ {
		got := row(e, cw)
		if !strings.Contains(got, "ctx 91% · 3 subagents") {
			t.Errorf("width %d dropped the count: %q", cw, got)
		}
		// "claude · ctx 91% · 3 subagents" takes 30.
		if keeps := strings.Contains(got, "claude"); keeps != (cw-4 >= 30) {
			t.Errorf("width %d kept the harness = %v: %q", cw, keeps, got)
		}
	}
	e.Subagents = 0
	if got := row(e, 40); strings.Contains(got, "subagent") || !strings.Contains(got, "claude · ctx 91%") {
		t.Errorf("a row with no subagents reads %q, want the harness and the warning alone", got)
	}
}
