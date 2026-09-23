package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestInboxBringsAnotherSessionsAgentToYou is the gap the Inbox closes, end to
// end against a real daemon and a real client: an agent in a session nobody
// is attached to blocks on an approval. The client attached elsewhere hears
// about it in the dock, naming the session; the leader chord i lists it under
// Approvals; enter lands on the pane in that session; and when the agent
// moves on, the item leaves the Inbox by itself.
//
// Negative control: with the attention hook cut from the daemon's event sink,
// nothing reaches the Inbox and the first wait fails.
func TestInboxBringsAnotherSessionsAgentToYou(t *testing.T) {
	term, base := attachClientBase(t)

	if out, err := tuiosCLI(t, base, "new", "e2e-fan", "--detach"); err != nil {
		t.Fatalf("create the second session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "e2e-fan", "needs_input",
		"--kind", "approval", "--harness", "claude-code", "-m", "approve Bash: go test ./..."); err != nil {
		t.Fatalf("set-agent-state in the other session: %v\n%s", err, out)
	}

	// The dock names the session, because the pane is not on screen.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "e2e-fan:") && strings.Contains(text, "needs approval")
	}, uiTimeout); err != nil {
		t.Fatalf("the dock never announced the other session's agent: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "inbox-dock")

	// The command line sees the same queue.
	out, err := tuiosCLI(t, base, "list-attention")
	if err != nil || !strings.Contains(out, "Approvals") || !strings.Contains(out, "approve Bash: go test ./...") {
		t.Fatalf("list-attention does not list the approval: %v\n%s", err, out)
	}

	// The leader chord opens the Inbox, grouped in words.
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "Inbox") && strings.Contains(text, "Approvals 1") &&
			strings.Contains(text, "approve Bash: go test") && strings.Contains(text, "e2e-fan")
	}, uiTimeout); err != nil {
		t.Fatalf("the Inbox never listed the approval: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "inbox-open")

	// Enter goes there: the client switches to the other session.
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("go to the item: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Approvals 1")
	}, uiTimeout); err != nil {
		t.Fatalf("enter did not close the Inbox: %v\n%s", err, term.Snapshot())
	}
	rows := waitForRows(t, base, func(rows []lsRow) bool {
		for _, r := range rows {
			if r.Name == "e2e-fan" && r.Attached {
				return true
			}
		}
		return false
	})
	switched := false
	for _, r := range rows {
		switched = switched || (r.Name == "e2e-fan" && r.Attached)
	}
	if !switched {
		t.Fatalf("enter did not switch the client to the other session: %+v\n%s", rows, term.Snapshot())
	}
	saveFrame(t, term, "inbox-jumped")

	// The agent moves on, and the item goes by itself.
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "e2e-fan", "working"); err != nil {
		t.Fatalf("set-agent-state working: %v\n%s", err, out)
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		out, _ = tuiosCLI(t, base, "list-attention")
		if strings.Contains(out, "Nothing is waiting for you.") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the approval was not closed when the agent left needs_input:\n%s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	alive(t, term, "after going to the other session from the Inbox")
}
