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

// BenchmarkSidebarAgentsFolded is the rebuild with the fold in force: the
// twelve-agent fleet with every row at rest past the threshold except those
// that never fold, so the fold's partition and its line are paid for.
func BenchmarkSidebarAgentsFolded(b *testing.B) {
	m, tree := benchAgentOS(b)
	m.Settings.SidebarAgentRestFold = time.Hour
	m.SidebarAgentsSeen = true
	old := time.Now().Add(-3 * time.Hour).UnixNano()
	for i := range tree.Sessions {
		for j := range tree.Sessions[i].Children {
			tree.Sessions[i].Children[j].StateAt = old
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		m.sidebarPanelLinesForTree(tree)
	}
}

// noAgentRailOS is a rail over plain shells with the fold configured and no
// agent ever seen, on a clock that moves a minute every read.
func noAgentRailOS(tb testing.TB) *OS {
	tb.Helper()
	config.Global.SidebarEnabled = true
	config.Global.SidebarPosition = "left"
	config.Global.SidebarWidth = config.SidebarDefaultWidth
	tb.Cleanup(func() { config.Global.SidebarEnabled = false })
	wins := make([]*terminal.Window, 0, 6)
	for i := range 6 {
		wins = append(wins, &terminal.Window{ID: "sh" + strconv.Itoa(i), CustomName: "shell" + strconv.Itoa(i), Workspace: 1})
	}
	m := &OS{Settings: config.Global, Windows: wins, Width: 120, Height: 60, SessionName: "s", CurrentWorkspace: 1}
	m.Settings.SidebarAgentRestFold = time.Hour
	clock := time.Unix(1_800_000_000, 0)
	prev := sidebarFoldClock
	sidebarFoldClock = func() time.Time {
		clock = clock.Add(time.Minute)
		return clock
	}
	tb.Cleanup(func() { sidebarFoldClock = prev })
	return m
}

// BenchmarkSidebarNoAgentsCached is the steady state of a rail with no
// agents while minutes pass: every frame must be served from the cache, so
// the fold costs someone with no agents nothing.
func BenchmarkSidebarNoAgentsCached(b *testing.B) {
	m := noAgentRailOS(b)
	m.sidebarPanel()
	b.ReportAllocs()
	for b.Loop() {
		m.sidebarPanel()
	}
}

// TestSidebarNoAgentsStaysCachedAcrossMinutes is the guard the benchmark
// above measures: with no agent seen, a minute passing leaves the rail's
// signature alone and the cached rail is served.
func TestSidebarNoAgentsStaysCachedAcrossMinutes(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	m := noAgentRailOS(t)
	m.sidebarPanel()
	sig := m.sidebarCache.sig
	for range 10 {
		m.sidebarPanel()
		if !m.sidebarCache.valid || m.sidebarCache.sig != sig {
			t.Fatal("the rail was rebuilt as minutes passed with no agent seen")
		}
	}
}
