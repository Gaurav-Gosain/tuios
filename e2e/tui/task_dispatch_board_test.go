package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestTheBoardTurnsDispatchOnAndPausesIt opens the board in a session with a
// fake agent that holds a card. The header says Dispatch Manual; ctrl+t turns
// the session's board to pull, and the agent's finish then gives it the next
// card; ctrl+x pauses all dispatch and the header says Paused; ctrl+x again
// starts it.
//
// How this could pass wrongly: the header word could change with no change
// on the daemon, so each step reads tuios task dispatch back; the next card
// could be taken by the test itself, so its holder must be the agent's pane
// and the card must say dispatched.
//
// Negative control (NEGATIVE_CONTROLS.md): with the chord's action not
// routed in the cockpit's keys, the header never says Pull.
func TestTheBoardTurnsDispatchOnAndPausesIt(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	mustCLI(t, base, "new", "home", "--detach")
	waitForSessionInfo(t, base, "home")
	worker := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", "home", "worker", "--no-focus", "--print-id"))
	mustCLI(t, base, "set-agent-state", "-s", "home", "-w", worker, "working", "--harness", "claude-code", "-m", "fixing the retry")
	mustCLI(t, base, "task", "add", "-s", "home", "-p", "Make the retry back off.", "Fix the retry")
	mustCLI(t, base, "task", "add", "-s", "home", "-p", "Write the retry docs.", "Write the docs")
	mustCLI(t, base, "task", "add", "-s", "home", "--ask-first", "Ship the release")
	mustCLI(t, base, "task", "claim", "t1", "-s", "home", "-w", worker)
	term := attachIn(t, base, "home", startOpts{cols: 160, rows: 45, shippedLooks: true})

	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the agents view", "b board")
	if err := term.SendKeys("b"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the board's dispatch", "Dispatch Manual")
	saveCockpitFrames(t, term, "board-dispatch-manual-160x45")

	if err := term.SendKeys(tuitest.Ctrl('t')); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the board in pull mode", "Dispatch Pull")
	if got := mustCLI(t, base, "task", "dispatch", "-s", "home"); !strings.Contains(got, "pull   session:home") {
		t.Fatalf("ASSERTION: ctrl+t did not set the daemon's board to pull:\n%s", got)
	}

	mustCLI(t, base, "task", "done", "t1", "-s", "home")
	holder, _, raw := waitForCards(t, base, "home", "the agent's finish did not give it t2",
		func(h, _ map[string]string) bool { return h["t2"] != "" })
	if holder["t2"] != worker || holder["t3"] != "" || !strings.Contains(raw, "dispatched") {
		t.Fatalf("ASSERTION: t2 is held by %q (want %s), t3 by %q (want none):\n%s", holder["t2"], worker, holder["t3"], raw)
	}
	waitText(t, term, "the card the agent was given, the ask-first card and the chords", "Write the docs", "free, ask first", "dispatch", "pause")
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "board-dispatch-pull-160x45")

	if err := term.SendKeys(tuitest.Ctrl('x')); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the paused header", "Paused")
	if got := mustCLI(t, base, "task", "dispatch"); !strings.Contains(got, "Dispatch: paused by human") {
		t.Fatalf("ASSERTION: ctrl+x did not pause dispatch:\n%s", got)
	}
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "board-dispatch-paused-160x45")

	if err := term.SendKeys(tuitest.Ctrl('x')); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "dispatch on again", "Dispatch Pull")
	if got := mustCLI(t, base, "task", "dispatch"); !strings.Contains(got, "Dispatch: on") {
		t.Fatalf("ASSERTION: the second ctrl+x did not start dispatch again:\n%s", got)
	}
	if strings.Contains(term.Screen().Text(), "Paused") {
		t.Errorf("ASSERTION: the header still says Paused\n%s", term.Snapshot())
	}
}
