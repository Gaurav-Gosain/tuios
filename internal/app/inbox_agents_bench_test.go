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
func benchAgents200OS(b *testing.B) *OS {
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
	e, ok := m.inboxAgentSelected()
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
