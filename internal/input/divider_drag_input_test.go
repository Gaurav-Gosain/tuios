package input

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TestDividerDragThroughTheMouseHandlers drives a divider drag the way the
// terminal delivers it: a press on the grip, drag reports below it, a release.
// The block above the footer has to give up rows and the split has to be
// recorded, or the gesture is one the mouse handlers never routed.
func TestDividerDragThroughTheMouseHandlers(t *testing.T) {
	m := hoverOS(t)
	for i, w := range m.Windows {
		w.AgentState = "working"
		m.Windows[i] = w
	}
	for _, id := range []string{"cccccccc3333", "dddddddd4444", "eeeeeeee5555", "ffffffff6666"} {
		m.Windows = append(m.Windows, &terminal.Window{ID: id, CustomName: "agent-" + id[:1], X: 31, Y: 1, Width: 40, Height: 20, Workspace: 1, AgentState: "working"})
	}
	lines := frameLines(m)
	x, y := railCell(t, lines, "───")
	agentsBefore := -1
	for i, line := range lines {
		if strings.Contains(stripSGR(line), "agents") && strings.Index(stripSGR(line), "agents") < 30 {
			agentsBefore = i
		}
	}
	if agentsBefore != y+1 {
		t.Fatalf("the grip is on row %d and the agents header on %d; want the header directly under it", y, agentsBefore)
	}

	m = pressed(m, x+1, y)
	if !m.SidebarSplitActive() {
		t.Fatal("a press on the divider did not arm the drag")
	}
	for i := 1; i <= 4; i++ {
		m = dragged(m, x+1, y+i)
	}
	if m.SidebarSectionSplit == 0 {
		t.Fatal("the drag recorded no split")
	}
	m = released(m, x+1, y+4)
	if m.SidebarSplitActive() {
		t.Fatal("the release did not end the drag")
	}
	after := -1
	for i, line := range frameLines(m) {
		if idx := strings.Index(stripSGR(line), "agents"); idx >= 0 && idx < 30 {
			after = i
		}
	}
	if after <= agentsBefore {
		t.Fatalf("after the drag the agents header is on row %d, was %d; want it lower", after, agentsBefore)
	}
}
