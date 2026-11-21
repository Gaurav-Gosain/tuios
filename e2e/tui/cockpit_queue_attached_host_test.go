package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestCockpitQueueWhileAttachedThroughAHost queues a reply from the agents
// view of a client attached to a session on a linked machine, alpha, for an
// agent in that session. The queue lives where the pane runs, so the reply
// must go to alpha's daemon, show as queued, and be typed when the agent
// rests.
//
// The ways it can go wrong, each an assertion: the reply is refused, as this
// machine's daemon has no such session (queue-prompt and list-queued sent
// here, not over the link); the reply is accepted and never listed, so the
// thread drops its echo; the reply stays queued after the agent rests.
func TestCockpitQueueWhileAttachedThroughAHost(t *testing.T) {
	f := startGlobalFleet(t)
	for _, args := range [][]string{
		{"set-window", "-s", "far", "-w", "0", "--name", "far-fix"},
		{"set-agent-state", "working", "-s", "far", "-w", "far-fix", "--harness", "claude-code"},
	} {
		if out, err := cliWithin(t, f.alpha, f.env, args...); err != nil {
			t.Fatalf("%v on alpha: %v\n%s", args, err, out)
		}
	}
	term := startIn(t, f.here, startOpts{args: []string{"attach", "--host", "alpha", "far"}, cols: 140, rows: 44,
		env: append([]string{"TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0"}, f.env...)})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached through alpha: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	waitText(t, term, "the far agent", "far-fix")
	_ = term.WaitStable(uiTimeout)

	sendReply(t, term, "Check the far build.")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "Check the far build.", "Queued, typed when it rests")
	}, uiTimeout); err != nil {
		saveCockpitFrames(t, term, "attached-queue-refused")
		t.Fatalf("ASSERTION: the reply to the agent on alpha does not show as queued: %v\n%s", err, term.Snapshot())
	}
	if ls, _ := cliWithin(t, f.alpha, f.env, "queue", "ls", "-s", "far"); !strings.Contains(ls, "Check the far build.") {
		saveCockpitFrames(t, term, "attached-queue-not-listed")
		t.Fatalf("ASSERTION: the reply is not in alpha's queue:\n%s\n%s", ls, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	if !strings.Contains(term.Screen().Text(), "Queued, typed when it rests") {
		saveCockpitFrames(t, term, "attached-queue-echo-dropped")
		t.Fatalf("ASSERTION: the queued echo went away while the reply waits on alpha:\n%s", term.Snapshot())
	}
	saveCockpitFrames(t, term, "attached-queue-queued")

	if out, err := cliWithin(t, f.alpha, f.env, "set-agent-state", "idle", "-s", "far", "-w", "far-fix", "--harness", "claude-code"); err != nil {
		t.Fatalf("set the agent at rest on alpha: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(_ tuitest.Screen) bool {
		out, _ := cliWithin(t, f.alpha, f.env, "capture-pane", "-s", "far", "-w", "far-fix")
		return strings.Contains(out, "Check the far build.")
	}, uiTimeout); err != nil {
		out, _ := cliWithin(t, f.alpha, f.env, "capture-pane", "-s", "far", "-w", "far-fix")
		t.Fatalf("ASSERTION: the queued reply was never typed on alpha at rest: %v\n%s", err, out)
	}
	if out, err := cliWithin(t, f.alpha, f.env, "set-agent-state", "working", "-s", "far", "-w", "far-fix", "--harness", "claude-code"); err != nil {
		t.Fatalf("set the agent to work on alpha: %v\n%s", err, out)
	}
	var ls string
	if err := term.WaitFor(func(_ tuitest.Screen) bool {
		ls, _ = cliWithin(t, f.alpha, f.env, "queue", "ls", "-s", "far")
		return strings.Contains(ls, "Nothing is queued")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the typed reply stayed in alpha's queue: %v\n%s", err, ls)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Queued, typed when it rests")
	}, uiTimeout); err != nil {
		saveCockpitFrames(t, term, "attached-queue-echo-stuck")
		t.Fatalf("ASSERTION: the echo still says queued after alpha typed the reply: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "attached-queue-typed")
	alive(t, term, "after the queued reply on alpha")
}
