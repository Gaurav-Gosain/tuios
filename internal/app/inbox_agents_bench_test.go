package app

import (
	"strconv"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// benchAgents200OS is a client watching two hundred agents in the attached
// session, in every state, with the cockpit open on them.
func benchAgents200OS(b testing.TB) *OS {
	b.Helper()
	config.Global.SidebarEnabled = true
	b.Cleanup(func() { config.Global.SidebarEnabled = false })
	now := time.Now().Add(-5 * time.Minute).UnixNano()
	wins := make([]*terminal.Window, 0, 200)
	for i := range 200 {
		wins = append(wins, &terminal.Window{
			ID: "agent" + strconv.Itoa(i), CustomName: "task" + strconv.Itoa(i), Workspace: 1,
			AgentState: benchAgentStates[i%len(benchAgentStates)], AgentHarness: "claude-code",
			AgentMessage: "editing files", AgentStateAt: now - int64(i)*int64(time.Second),
			AgentQueued: i % 3,
		})
	}
	m := &OS{Settings: config.Global, Windows: wins, Width: 160, Height: 60, SessionName: "s", CurrentWorkspace: 1}
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: benchInboxItems()})
	m.OpenInbox("")
	m.Inbox.agents.on = true
	benchThread(m)
	return m
}

// benchThread gives the selected agent a read transcript of two hundred
// entries, the most a thread keeps, so a frame draws a full conversation.
func benchThread(m *OS) {
	m.renderInbox()
	e, ok := m.cockpitAgent()
	if !ok {
		return
	}
	t := m.thread(e)
	t.read = true
	for i := range 200 {
		en := threadEntry{ID: strconv.Itoa(i), Role: "assistant", Kind: "text",
			Text: "Reading the handler and the tests around it before changing the retry loop, entry " + strconv.Itoa(i) + "."}
		switch i % 4 {
		case 0:
			en.Role = "user"
		case 1:
			en.Kind, en.Tool, en.Target = "tool_call", "Read", "internal/app/inbox_thread.go"
		}
		t.entries = append(t.entries, en)
	}
	t.gen++
}

// BenchmarkInboxAgents200 is a frame of the cockpit over two hundred
// agents with nothing changed since the last one: the steady state while it
// is on screen. The design's budget is 1 ms and 200 allocations.
func BenchmarkInboxAgents200(b *testing.B) {
	m := benchAgents200OS(b)
	m.renderInbox()
	b.ReportAllocs()
	for b.Loop() {
		m.renderInbox()
	}
}

// BenchmarkInboxAgents200Rebuild is a frame after an agent changed state: the
// list and the fleet built again from the session tree, and the frame drawn
// again.
func BenchmarkInboxAgents200Rebuild(b *testing.B) {
	m := benchAgents200OS(b)
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		w := m.Windows[i%len(m.Windows)]
		w.AgentStateAt++
		m.Inbox.Gen++
		m.renderInbox()
		i++
	}
}

// BenchmarkInboxAgents200OneChange is the frame after one agent changed state,
// the common case: the list and the frame are built again.
func BenchmarkInboxAgents200OneChange(b *testing.B) {
	m := benchAgents200OS(b)
	m.renderInbox()
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		m.Windows[i%16].AgentStateAt++
		m.Inbox.Gen++
		m.renderInbox()
		i++
	}
}

// BenchmarkInboxAgents200Move is a frame after the cursor moved one row: the
// rows stay, and only the frame is drawn again.
func BenchmarkInboxAgents200Move(b *testing.B) {
	m := benchAgents200OS(b)
	m.renderInbox()
	b.ReportAllocs()
	d := 1
	for i := 0; b.Loop(); i++ {
		if i%50 == 0 {
			d = -d
		}
		m.CockpitMove(d)
		m.renderInbox()
	}
}

// benchDiffThread gives the selected agent a transcript of edits, each with
// a diff of four hundred lines, the most the daemon sends, and one more
// entry per loop: the thread an agent streams into while the view is open.
func benchDiffThread(b *testing.B) (*OS, *agentThread) { return benchDiffThreadT(b) }

func benchDiffThreadT(b testing.TB) (*OS, *agentThread) {
	m := benchAgents200OS(b)
	// The cursor on an agent with no item, so the focus is its thread.
	for _, r := range m.cockpitRows() {
		if r.kind == ckRowAgent && !r.hasItem {
			m.cockpitSelectKey(r.key)
			break
		}
	}
	e, _ := m.cockpitAgent()
	t := m.thread(e)
	t.read = true
	t.entries = t.entries[:0]
	for i := range 40 {
		d := &threadDiff{File: "internal/app/file" + strconv.Itoa(i) + ".go", Added: 400}
		h := threadHunk{OldStart: 1, NewStart: 1}
		for j := range 400 {
			h.Lines = append(h.Lines, threadDiffLine{Op: "+", Text: "\tif err := do(ctx, " + strconv.Itoa(j) + "); err != nil { return err }"})
		}
		d.Hunks = []threadHunk{h}
		t.entries = append(t.entries, threadEntry{ID: "e" + strconv.Itoa(i), Role: "assistant", Kind: "tool_call", Tool: "Edit", Target: d.File, Diff: d})
	}
	t.gen++
	m.Inbox.agents.on = true
	return m, t
}

// BenchmarkCockpitThreadGrows is a frame after the thread of edits grew by
// one entry: the cards built again, the frame drawn again.
func BenchmarkCockpitThreadGrows(b *testing.B) {
	m, t := benchDiffThread(b)
	m.renderInbox()
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		t.entries = append(t.entries, threadEntry{ID: "n" + strconv.Itoa(i), Role: "assistant", Kind: "text", Text: "Next step."})
		t.gen++
		m.renderInbox()
		i++
	}
}
