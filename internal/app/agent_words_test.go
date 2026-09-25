package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/x/ansi"
)

// TestOneSessionNameEverywhere: a session with a display name is called by it
// in the Inbox rows, the peek, the dock's alerts, the palette and the close
// dialog, as the rail calls it. They said "session-0" while the rail said
// "demo".
func TestOneSessionNameEverywhere(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	m.SessionName = "session-0"
	m.SessionDisplayName = "demo"

	it := item("1", session.AttentionApproval, "session-0", "w-1", "approve Bash: make", 1)
	if got := m.inboxWhere(it); got != "demo" {
		t.Errorf("Inbox row names the session %q, want the rail's \"demo\"", got)
	}
	if got := m.inboxAlertText([]session.AttentionItem{it}); !strings.HasPrefix(got, "demo: ") {
		t.Errorf("dock alert = %q, want it to name the session \"demo\"", got)
	}
	// Another machine's session keeps the name that machine sent.
	far := it
	far.Host = "build"
	if got := m.inboxWhere(far); got != "build:session-0" {
		t.Errorf("a far item names %q, want build:session-0", got)
	}

	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{it}})
	m.OpenInbox("")
	out, _, _ := m.renderInbox()
	if plain := ansi.Strip(out); strings.Contains(plain, "session-0") || !strings.Contains(plain, "demo") {
		t.Errorf("the Inbox does not call the session demo:\n%s", plain)
	}

	var sessionRow string
	for _, p := range getSessionPaletteItems(m) {
		if strings.HasPrefix(p.Name, "Session: ") {
			sessionRow = p.Name
		}
	}
	if !strings.HasSuffix(sessionRow, "demo") {
		t.Errorf("palette session row = %q, want it to end in demo", sessionRow)
	}

	if got := m.sessionTitle("session-0"); got != "demo" {
		t.Errorf("sessionTitle = %q, want demo", got)
	}
	if got := m.sessionTitle("other"); got != "other" {
		t.Errorf("sessionTitle of an unlabelled session = %q, want its name", got)
	}
}
