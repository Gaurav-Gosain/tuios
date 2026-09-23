package app

import (
	"strconv"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// benchInboxItems is a sixteen-way fan waiting on the person: one of each
// kind across sixteen sessions.
func benchInboxItems() []session.AttentionItem {
	now := time.Now()
	items := make([]session.AttentionItem, 0, 16)
	for i := range 16 {
		kind := session.AttentionKindNames[i%len(session.AttentionKindNames)]
		items = append(items, session.AttentionItem{
			ID: strconv.Itoa(i + 1), Kind: kind, Session: "fan-" + strconv.Itoa(i),
			Window: "w" + strconv.Itoa(i), Name: "task" + strconv.Itoa(i),
			Summary: "approve Bash: go test ./internal/...", Since: now.Add(-time.Duration(i) * time.Minute).UnixNano(),
		})
	}
	return items
}

// BenchmarkSidebarAgentsRebuildWithInbox is BenchmarkSidebarAgentsRebuild with
// a live Inbox of sixteen items, which is what the agents header counts then.
func BenchmarkSidebarAgentsRebuildWithInbox(b *testing.B) {
	m, tree := benchAgentOS(b)
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: benchInboxItems()})
	b.ReportAllocs()
	for b.Loop() {
		m.sidebarPanelLinesForTree(tree)
	}
}

// BenchmarkSidebarAgentsCachedWithInbox is the steady state with the Inbox
// live: the signature folds its generation, and nothing else.
func BenchmarkSidebarAgentsCachedWithInbox(b *testing.B) {
	m, _ := benchAgentOS(b)
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: benchInboxItems()})
	m.sidebarPanel()
	b.ReportAllocs()
	for b.Loop() {
		m.sidebarPanel()
	}
}

// BenchmarkInboxRender draws the open Inbox over a sixteen-item queue.
func BenchmarkInboxRender(b *testing.B) {
	m, _ := benchAgentOS(b)
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: benchInboxItems()})
	m.OpenInbox("")
	b.ReportAllocs()
	for b.Loop() {
		m.renderInbox()
	}
}

// BenchmarkInboxApplyBurst folds a sixteen-event burst into the mirror, the
// work a fan finishing together costs the client.
func BenchmarkInboxApplyBurst(b *testing.B) {
	m, _ := benchAgentOS(b)
	m.IsDaemonSession = true
	items := benchInboxItems()
	evs := make([]InboxEvent, len(items))
	b.ReportAllocs()
	for b.Loop() {
		m.Inbox.Items = m.Inbox.Items[:0]
		for i := range items {
			evs[i] = InboxEvent{Action: session.AttentionUpdated, Item: &items[i]}
		}
		m.applyInboxEvents(InboxEventsMsg{Events: evs})
	}
}
