package tuie2e

import (
	"github.com/Gaurav-Gosain/tuitest"
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

// TestNarrowRailKeepsTheAgentNameBesideAQueue: on the rail an 80 column
// screen gets, "1 queued" used to leave "a…" of an agent called agent. The
// name wins: the figure shortens or goes before the name is cut. The frame
// is saved under artifactDir.
//
// How this could pass wrongly, written down first: "agent" is also the
// session's name, so the row read is the one under the agents section's
// header and inside the rail's columns; and the row might be drawn before the
// queue reaches it, so the test waits for its queue figure first.
func TestNarrowRailKeepsTheAgentNameBesideAQueue(t *testing.T) {
	const cols, rows = 80, 24
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	if out, err := tuiosCLI(t, base, "new", "agent", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "agent", "working", "--harness", "claude-code"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "agent", "--name", "agent"); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "queue", "-s", "agent", "also add a CHANGELOG entry"); err != nil {
		t.Fatalf("queue: %v\n%s", err, out)
	}
	term := attachIn(t, base, "agent", startOpts{cols: cols, rows: rows, shippedLooks: true})
	railCol := cols - narrowRailWidth
	railText := func(s tuitest.Screen, y int) string {
		var b strings.Builder
		for x := railCol; x < cols; x++ {
			b.WriteString(s.Cell(x, y).Content)
		}
		return b.String()
	}
	// The row under the agents section's header, which is the agent's.
	agentRow := func(s tuitest.Screen) string {
		for y := range rows - 1 {
			if strings.HasPrefix(strings.TrimSpace(strings.Trim(railText(s, y), "│ ")), "agents") {
				return railText(s, y+1)
			}
		}
		return ""
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(agentRow(s), "q") }, uiTimeout); err != nil {
		t.Fatalf("the rail never listed the agent: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "rail-80x24")
	if row := agentRow(term.Screen()); !strings.Contains(row, "agent") {
		t.Errorf("the rail cut the agent's name for its queue: %q\n%s", row, term.Snapshot())
	}
}
