package app

import (
	"strconv"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// benchAgentStates cycles through every state a rail row can be in, so the
// benchmarks below pay for each branch of the row renderer.
var benchAgentStates = []string{"working", "needs_input", "done", "idle", "errored", "unknown"}

// benchAgentOS is a rail watching twelve agents in the attached session, in
// every state, with harnesses and notes, which is the fleet the agents section
// is for.
func benchAgentOS(b *testing.B) (*OS, sessiontree.Tree) {
	b.Helper()
	config.Global.SidebarEnabled = true
	config.Global.SidebarPosition = "left"
	config.Global.SidebarWidth = config.SidebarDefaultWidth
	b.Cleanup(func() { config.Global.SidebarEnabled = false })

	now := time.Now().Add(-5 * time.Minute).UnixNano()
	wins := make([]*terminal.Window, 0, 12)
	inputs := make([]sessiontree.WindowInput, 0, 12)
	for i := range 12 {
		id := "agent" + strconv.Itoa(i)
		state := benchAgentStates[i%len(benchAgentStates)]
		w := &terminal.Window{
			ID: id, CustomName: "task" + strconv.Itoa(i), Workspace: 1,
			AgentState: state, AgentHarness: "claude-code", AgentMessage: "editing files", AgentStateAt: now,
		}
		wins = append(wins, w)
		inputs = append(inputs, sessiontree.WindowInput{
			ID: id, Title: w.CustomName, AgentState: state, Harness: w.AgentHarness,
			Message: w.AgentMessage, StateAt: now, Workspace: 1,
		})
	}
	m := &OS{Settings: config.Global, Windows: wins, Width: 120, Height: 60, SessionName: "s", CurrentWorkspace: 1}
	tree := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "s", Attached: true, IsCurrent: true, CurrentWorkspace: 1, Windows: inputs},
	})
	return m, tree
}

// BenchmarkSidebarAgentsRebuild is the cost of a frame the cache cannot serve:
// every row of a twelve-agent rail filtered, sorted and styled from scratch.
// It is what a state change anywhere in the fleet costs.
func BenchmarkSidebarAgentsRebuild(b *testing.B) {
	m, tree := benchAgentOS(b)
	b.ReportAllocs()
	for b.Loop() {
		m.sidebarPanelLinesForTree(tree)
	}
}

// BenchmarkSidebarAgentsCached is the steady state with the same fleet: the
// signature is folded over every agent pane on every frame, so what the fold
// reads is on the hot path even when nothing is redrawn.
func BenchmarkSidebarAgentsCached(b *testing.B) {
	m, _ := benchAgentOS(b)
	m.sidebarPanel() // prime the cache
	b.ReportAllocs()
	for b.Loop() {
		m.sidebarPanel()
	}
}
