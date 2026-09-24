package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/x/ansi"
)

// TestOneWordPerState pins the phrase each state is spelled with. needs_input
// was "need input", "needs input", "waiting on you" and "blocked"; done was
// also "finished"; unknown passed for idle.
func TestOneWordPerState(t *testing.T) {
	for state, want := range map[string]string{
		"working":     "working",
		"needs_input": "needs you",
		"idle":        "idle",
		"done":        "done",
		"errored":     "errored",
		"unknown":     "unknown",
	} {
		if got := sidebarStateWords(state); got != want {
			t.Errorf("sidebarStateWords(%q) = %q, want %q", state, got, want)
		}
	}
	if word, _ := agentTransitionNotice("needs_input"); word != "needs you" {
		t.Errorf("the needs-input alert says %q, want \"needs you\"", word)
	}
	if word, _ := agentTransitionNotice("done"); word != "done" {
		t.Errorf("the done alert says %q, want \"done\"", word)
	}
	if got := inboxKindWords(session.AttentionItem{Kind: session.AttentionFinished}); got != "done" {
		t.Errorf("a finished Inbox item says %q, want \"done\"", got)
	}
	if got := inboxKindWords(session.AttentionItem{Kind: session.AttentionAsk}); got != inboxKindWords(session.AttentionItem{Kind: session.AttentionQuestion}) {
		t.Errorf("an ask-human question says %q, unlike an agent's question", got)
	}
}

// TestNeedsYouAgreesWithItsCount is the grammar the badge tooltip got wrong:
// "1 agent need input".
func TestNeedsYouAgreesWithItsCount(t *testing.T) {
	for _, tc := range []struct {
		n    int
		want string
	}{
		{1, "1 agent needs you"},
		{2, "2 agents need you"},
	} {
		if got := sidebarTooltipBadgeLabel(sidebarStripBadgeInfo{Count: tc.n, State: "needs_input"}); got != tc.want {
			t.Errorf("badge tooltip for %d = %q, want %q", tc.n, got, tc.want)
		}
	}
	if got := (sidebarAgentCountInfo{Blocked: 1, Done: 2}).words(); got != "1 needs you"+sidebarAgentSep()+"2 done" {
		t.Errorf("header count = %q", got)
	}
	if got := (sidebarAgentCountInfo{Blocked: 3}).words(); got != "3 need you" {
		t.Errorf("header count = %q", got)
	}
}

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
