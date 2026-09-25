package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/x/ansi"
)

// inboxRowIDs is the ids of the item rows the overlay would draw.
func inboxRowIDs(m *OS) []string {
	var ids []string
	for _, r := range m.inboxRows() {
		if r.item != nil {
			ids = append(ids, r.item.ID)
		}
	}
	return ids
}

// typeSelector opens the line, which starts with the selector in force,
// clears it and types text.
func typeSelector(m *OS, text string) {
	m.InboxStartSelect()
	for range len(m.Inbox.selectDraft) {
		m.InboxSelectBackspace()
	}
	for _, r := range text {
		m.InboxSelectType(string(r))
	}
	m.InboxSelectApply()
}

// TestInboxSelectNarrowsTheList: a selector typed on / keeps the items it
// matches, a heading counts only those, the title names the selector in
// words, and an empty line puts every item back.
func TestInboxSelectNarrowsTheList(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	codex := item("1", session.AttentionApproval, "api-fan-2", "w1", "approve", 10)
	codex.Harness = "codex"
	claude := item("2", session.AttentionApproval, "web", "w2", "approve", 20)
	claude.Harness = "claude-code"
	done := item("3", session.AttentionFinished, "api-fan-3", "w3", "", 30)
	done.Harness = "codex"
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{codex, claude, done}})
	m.OpenInbox("")

	typeSelector(m, "harness:codex needs:you")
	if got := strings.Join(inboxRowIDs(m), ","); got != "1" {
		t.Fatalf("harness:codex needs:you shows %s, want 1", got)
	}
	out, _, _ := m.renderInbox()
	plain := ansi.Strip(out)
	for _, want := range []string{"select harness:codex needs:you", "Approvals 1"} {
		if !strings.Contains(plain, want) {
			t.Errorf("the narrowed Inbox does not show %q:\n%s", want, plain)
		}
	}

	// A bare program name names its harness, and the session term globs.
	typeSelector(m, "harness:claude")
	if got := strings.Join(inboxRowIDs(m), ","); got != "2" {
		t.Errorf("harness:claude shows %s, want 2", got)
	}
	typeSelector(m, "session:api-fan-*")
	if got := strings.Join(inboxRowIDs(m), ","); got != "1,3" {
		t.Errorf("session:api-fan-* shows %s, want 1,3", got)
	}

	// The selector stays while the Inbox is closed and opened again.
	m.CloseInbox()
	m.OpenInbox("")
	if m.Inbox.Select != "session:api-fan-*" {
		t.Errorf("reopening dropped the selector: %q", m.Inbox.Select)
	}

	// The line opens with the selector in force, to be edited.
	m.InboxStartSelect()
	if m.Inbox.selectDraft != "session:api-fan-*" {
		t.Errorf("the line opened with %q, want the selector in force", m.Inbox.selectDraft)
	}
	typeSelector(m, "")
	if got := strings.Join(inboxRowIDs(m), ","); got != "1,2,3" {
		t.Errorf("an empty selector shows %s, want every item", got)
	}
}

// TestInboxSelectKeepsTheLineOpenOnABadSelector: a selector that does not
// parse is not applied, and the line says why.
func TestInboxSelectKeepsTheLineOpenOnABadSelector(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{item("1", session.AttentionErrored, "a", "w1", "", 1)}})
	m.OpenInbox("")
	typeSelector(m, "colour:red")
	if !m.InboxSelecting() || m.Inbox.Select != "" {
		t.Fatalf("a bad selector was applied (open=%v select=%q)", m.InboxSelecting(), m.Inbox.Select)
	}
	out, _, _ := m.renderInbox()
	if plain := ansi.Strip(out); !strings.Contains(plain, "Not a selector") || !strings.Contains(plain, "select colour:red") {
		t.Errorf("the line does not say why:\n%s", plain)
	}
	m.InboxSelectCancel()
	if m.InboxSelecting() || len(inboxRowIDs(m)) != 1 {
		t.Error("esc did not close the line and leave the list as it was")
	}
}
