package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestAgentWorkKeysWaitForAnAgent drives the foundation of the agent review,
// triage and reply work against a real daemon and client. With no agent in
// view, the prefix menu offers neither ctrl+b v nor ctrl+b O. Once a pane
// reports an agent state it offers both. The keys that are bound ahead of
// their work change nothing: the chords leave the client as it was, and the
// Inbox's new keys leave the Inbox open on the same item.
//
// Negative control: with the two lines taken out of IsAgentPrefixKeybinding,
// the first check fails, because the menu shows them to a person who has
// never run an agent.
func TestAgentWorkKeysWaitForAnAgent(t *testing.T) {
	term, base := attachClientBase(t)

	openMenu := func() string {
		t.Helper()
		if err := term.SendKeys(tuitest.Ctrl('b')); err != nil {
			t.Fatalf("press the leader: %v", err)
		}
		if err := term.WaitForText("Toggle tiling", uiTimeout); err != nil {
			t.Fatalf("the prefix menu never opened: %v\n%s", err, term.Snapshot())
		}
		if err := term.WaitStable(uiTimeout); err != nil {
			t.Fatalf("the prefix menu never settled: %v\n%s", err, term.Snapshot())
		}
		return term.Screen().Text()
	}
	closeMenu := func() {
		t.Helper()
		if err := term.SendKeys(tuitest.Esc); err != nil {
			t.Fatalf("close the prefix menu: %v", err)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return !strings.Contains(s.Text(), "Toggle tiling")
		}, uiTimeout); err != nil {
			t.Fatalf("the prefix menu did not close: %v\n%s", err, term.Snapshot())
		}
		time.Sleep(insertGuard)
	}

	text := openMenu()
	if strings.Contains(text, "Review changes") || strings.Contains(text, "Newest finished") {
		t.Fatalf("the prefix menu offers the agent review keys before any agent was seen\n%s", term.Snapshot())
	}
	saveFrame(t, term, "agent-work-menu-no-agents")
	closeMenu()

	// A pane reports an agent waiting on an approval, which also gives the
	// Inbox an item.
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "e2e-ctrlp", "needs_input",
		"--kind", "approval", "--harness", "claude-code", "-m", "approve Bash: go test ./..."); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		text = openMenu()
		if strings.Contains(text, "Review changes") && strings.Contains(text, "Newest finished") {
			break
		}
		closeMenu()
		if time.Now().After(deadline) {
			t.Fatalf("the prefix menu never offered the agent review keys after an agent was seen\n%s", term.Snapshot())
		}
		time.Sleep(200 * time.Millisecond)
	}
	saveFrame(t, term, "agent-work-menu-agents")

	// v is bound ahead of its work: it runs and changes nothing on screen.
	if err := term.SendKeys("v"); err != nil {
		t.Fatalf("press v: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Toggle tiling")
	}, uiTimeout); err != nil {
		t.Fatalf("ctrl+b v left the prefix menu open: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)
	alive(t, term, "after ctrl+b v")

	// The Inbox's new keys leave it open on the same list.
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	if err := term.WaitForText("Approvals 1", uiTimeout); err != nil {
		t.Fatalf("the Inbox never listed the approval: %v\n%s", err, term.Snapshot())
	}
	for _, key := range []string{"z", "u", "S", "v", "n", "J", "K"} {
		if err := term.SendKeys(key); err != nil {
			t.Fatalf("press %s: %v", key, err)
		}
	}
	time.Sleep(insertGuard)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the Inbox never settled: %v\n%s", err, term.Snapshot())
	}
	if text := term.Screen().Text(); !strings.Contains(text, "Approvals 1") || !strings.Contains(text, "approve Bash: go test") {
		t.Fatalf("a key bound ahead of its work changed the Inbox\n%s", term.Snapshot())
	}
	saveFrame(t, term, "agent-work-inbox-unchanged")
	alive(t, term, "after the Inbox's new keys")
}
