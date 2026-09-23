package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestSelectorNarrowsTheInboxAndGuardsABroadcast drives selectors end to end
// against a real daemon and a real client: two agents in two sessions block on
// an approval, one codex and one Claude Code. / in the Inbox narrows it to the
// codex one, with the selector named in the title. On the command line a
// message by selector sends nothing until it is confirmed, and --yes sends it
// to exactly the matched pane.
//
// Negative control: with the selector line not opened on /, the Inbox keeps
// both rows and the second wait fails.
func TestSelectorNarrowsTheInboxAndGuardsABroadcast(t *testing.T) {
	term, base := attachClientBase(t)

	for _, s := range []struct{ name, harness string }{{"e2e-sel-codex", "codex"}, {"e2e-sel-claude", "claude-code"}} {
		if out, err := tuiosCLI(t, base, "new", s.name, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", s.name, err, out)
		}
		if out, err := tuiosCLI(t, base, "set-agent-state", "-s", s.name, "needs_input",
			"--kind", "approval", "--harness", s.harness, "-m", "approve Bash from "+s.harness); err != nil {
			t.Fatalf("set-agent-state in %s: %v\n%s", s.name, err, out)
		}
	}

	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "Approvals 2")
	}, uiTimeout); err != nil {
		t.Fatalf("the Inbox never listed both approvals: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys("/", "harness:codex", tuitest.Enter); err != nil {
		t.Fatalf("type the selector: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "[select harness:codex]") && strings.Contains(text, "Approvals 1") &&
			strings.Contains(text, "e2e-sel-codex") && !strings.Contains(text, "e2e-sel-claude")
	}, uiTimeout); err != nil {
		t.Fatalf("the selector did not narrow the Inbox to the codex agent: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "inbox-selected")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("close the Inbox: %v", err)
	}

	// The same selector on the command line.
	out, err := tuiosCLI(t, base, "list-agents", "--select", "harness:codex")
	if err != nil || !strings.Contains(out, "e2e-sel-codex") || strings.Contains(out, "e2e-sel-claude") || !strings.Contains(out, "--confirm") {
		t.Fatalf("list-agents --select did not list only the codex agent with a token: %v\n%s", err, out)
	}

	// Not at a terminal and not confirmed: nothing is sent, and the set is named.
	out, err = tuiosCLI(t, base, "send-agent-message", "--select", "harness:codex,claude-code", "rebase onto main")
	if err == nil || !strings.Contains(out, "Nothing was sent") || !strings.Contains(out, "e2e-sel-codex") || !strings.Contains(out, "e2e-sel-claude") {
		t.Fatalf("an unconfirmed send by selector did not refuse and list the set: %v\n%s", err, out)
	}
	out, _ = tuiosCLI(t, base, "read-agent-messages", "-s", "e2e-sel-codex", "--limit", "5")
	if strings.Contains(out, "rebase onto main") {
		t.Fatalf("an unconfirmed send reached the pane:\n%s", out)
	}

	out, err = tuiosCLI(t, base, "send-agent-message", "--select", "harness:codex", "--yes", "rebase onto main")
	if err != nil || !strings.Contains(out, "1 sent, 0 refused") {
		t.Fatalf("send by selector with --yes: %v\n%s", err, out)
	}
	out, _ = tuiosCLI(t, base, "read-agent-messages", "-s", "e2e-sel-codex", "--limit", "5")
	if !strings.Contains(out, "rebase onto main") {
		t.Fatalf("the confirmed message is not in the codex session's ring:\n%s", out)
	}
	out, _ = tuiosCLI(t, base, "read-agent-messages", "-s", "e2e-sel-claude", "--limit", "5")
	if strings.Contains(out, "rebase onto main") {
		t.Fatalf("the message reached a pane the selector did not match:\n%s", out)
	}
	alive(t, term, "after narrowing the Inbox by selector")
}
