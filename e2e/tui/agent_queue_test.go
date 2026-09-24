package tuie2e

import (
	"strings"
	"testing"
	"time"
)

// TestQueuedMessageIsTypedWhenTheAgentRests drives the delivery queue against
// a real daemon and client. A pane reports a working agent, and tuios queue
// leaves it a message: it is listed as waiting, get-agent-state counts it, and
// nothing reaches the pane. When the pane reports idle the message is typed
// into it, where the attached client shows it, and the queue is empty again.
//
// Negative control: with the noteQueueEvent call taken out of the session
// event sink, the message is never typed and the wait for it times out.
func TestQueuedMessageIsTypedWhenTheAgentRests(t *testing.T) {
	term, base := attachClientBase(t)
	const marker = "queued-reply-7f3a"

	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "e2e-ctrlp", "working"); err != nil {
		t.Fatalf("set-agent-state working: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "queue", "-s", "e2e-ctrlp", "echo", marker); err != nil {
		t.Fatalf("tuios queue: %v\n%s", err, out)
	} else if !strings.Contains(out, "typed when the agent comes to rest") {
		t.Errorf("tuios queue said %q, want it to wait for the agent", out)
	}
	out, err := tuiosCLI(t, base, "queue", "ls", "-s", "e2e-ctrlp")
	if err != nil || !strings.Contains(out, "waiting") || !strings.Contains(out, "echo "+marker) {
		t.Fatalf("tuios queue ls = %q (%v), want the waiting entry", out, err)
	}
	if out, err := tuiosCLI(t, base, "get-agent-state", "-s", "e2e-ctrlp", "--json"); err != nil || !strings.Contains(strings.ReplaceAll(out, " ", ""), `"queued":1`) {
		t.Errorf("get-agent-state = %q (%v), want queued 1", out, err)
	}
	time.Sleep(time.Second)
	if strings.Contains(term.Screen().Text(), marker) {
		t.Fatalf("the message was typed into a working agent\n%s", term.Snapshot())
	}

	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "e2e-ctrlp", "idle"); err != nil {
		t.Fatalf("set-agent-state idle: %v\n%s", err, out)
	}
	if err := term.WaitForText(marker, uiTimeout); err != nil {
		t.Fatalf("the queued message was never typed at rest: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "agent-queue-typed")
	deadline := time.Now().Add(uiTimeout)
	for {
		out, err := tuiosCLI(t, base, "queue", "ls", "-s", "e2e-ctrlp")
		if err == nil && strings.Contains(out, "Nothing is queued") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the typed message stayed queued: %q (%v)", out, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	alive(t, term, "after the queued message was typed")
}
