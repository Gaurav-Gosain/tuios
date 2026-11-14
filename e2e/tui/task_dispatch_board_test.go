package tuie2e

import (
	"os"
	"path/filepath"
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
	// "Dispatch Pull" is on the paused header too, so the wait is for the
	// header the daemon's push of the resume draws: Pull without Paused.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		txt := s.Text()
		return strings.Contains(txt, "Dispatch Pull") && !strings.Contains(txt, "Paused")
	}, uiTimeout); err != nil {
		t.Errorf("ASSERTION: the header still says Paused after the resume: %v\n%s", err, term.Snapshot())
	}
	if got := mustCLI(t, base, "task", "dispatch"); !strings.Contains(got, "Dispatch: on") {
		t.Fatalf("ASSERTION: the second ctrl+x did not start dispatch again:\n%s", got)
	}
}

// TestTheBoardTurnsOnAuto opens the board of a repository's cards with no
// agent running. ctrl+t twice turns the board from manual to pull to auto.
// The daemon then starts Claude Code for the oldest open card, in a new
// worktree, and holds the next one back at the cap of 1; the header says
// Dispatch Auto, 1 running, 1 waiting for a free machine. The ask-first card
// waits for a person. ctrl+t once more turns the board back to manual.
//
// How this could pass wrongly: the header could change with no change on the
// daemon, so each step reads tuios task dispatch back; the running count
// could be a client guess, so the card must be held and say dispatched.
//
// Negative control (NEGATIVE_CONTROLS.md): with ctrl+t turning pull back to
// manual, as before auto, the header never says Auto.
func TestTheBoardTurnsOnAuto(t *testing.T) {
	base, origin := fanFixture(t)
	t.Setenv("TUIOS_WORKTREE_DIR", "")
	claudeDir, _ := poolAgentBin(t)
	t.Setenv("PATH", claudeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	useShippedLooks(base)
	here := filepath.Join(base, "dev", "api")
	cloneWithOrigin(t, origin, here, "git@github.com:acme/api.git")
	mustCLI(t, base, "new", "home", "--detach", "--cwd", here)
	waitForSessionInfo(t, base, "home")
	const proj = "git@github.com:acme/api.git"
	mustCLI(t, base, "task", "add", "-s", "home", "--project", proj, "-p", "Make the client retry on a 503.", "Fix the retry")
	mustCLI(t, base, "task", "add", "-s", "home", "--project", proj, "-p", "Say how retries work.", "Write the retry docs")
	mustCLI(t, base, "task", "add", "-s", "home", "--project", proj, "--ask-first", "Ship the release")
	mustCLI(t, base, "task", "dispatch", "cap", "1")
	term := attachIn(t, base, "home", startOpts{cols: 160, rows: 45, shippedLooks: true})

	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the agents view", "b board")
	if err := term.SendKeys("b"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the board's dispatch", "Dispatch Manual")
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "board-auto-before-160x45")

	if err := term.SendKeys(tuitest.Ctrl('t')); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the board in pull mode", "Dispatch Pull")
	if err := term.SendKeys(tuitest.Ctrl('t')); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the board in auto mode", "Dispatch Auto")
	if got := mustCLI(t, base, "task", "dispatch"); !strings.Contains(got, "auto   github.com/acme/api, starts claude on this machine") {
		t.Fatalf("ASSERTION: the second ctrl+t did not set the daemon's board to auto with claude:\n%s", got)
	}
	cards, raw := waitAutoCards(t, base, "home", "auto did not start an agent for t1",
		func(c map[string]autoCard) bool { return held(c, "t1") })
	if !cards["t1"].Dispatched || held(cards, "t2") || held(cards, "t3") {
		t.Fatalf("ASSERTION: want t1 held by dispatch and t2, t3 free:\n%s", raw)
	}
	waitText(t, term, "the running and waiting counts", "1 running, 1 waiting for a free machine")
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "board-auto-after-160x45")

	if err := term.SendKeys(tuitest.Ctrl('t')); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the board back to manual", "Dispatch Manual")
	if got := mustCLI(t, base, "task", "dispatch"); strings.Contains(got, "auto   ") {
		t.Fatalf("ASSERTION: the third ctrl+t left the board in auto:\n%s", got)
	}
}

// TestTheBoardCountsWhatAutoStarts turns a board to auto with room for every
// open card: two cards, a cap of 2, and an ask-first card that waits for a
// person. The daemon starts an agent for each open card, and no card waits,
// so the only change the header can show is the run count. It must say
// "2 running" while the two agents hold their cards. In the film of v0.13.0
// it said "0 running" while three agents ran, until another dispatch event.
//
// Negative control (NEGATIVE_CONTROLS.md): with autoPass publishing only a
// change of waiting cards and notes, as before, the header stays at
// "0 running".
func TestTheBoardCountsWhatAutoStarts(t *testing.T) {
	base, origin := fanFixture(t)
	t.Setenv("TUIOS_WORKTREE_DIR", "")
	claudeDir, _ := poolAgentBin(t)
	t.Setenv("PATH", claudeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	useShippedLooks(base)
	here := filepath.Join(base, "dev", "api")
	cloneWithOrigin(t, origin, here, "git@github.com:acme/api.git")
	mustCLI(t, base, "new", "home", "--detach", "--cwd", here)
	waitForSessionInfo(t, base, "home")
	const proj = "git@github.com:acme/api.git"
	mustCLI(t, base, "task", "add", "-s", "home", "--project", proj, "-p", "Make the client retry on a 503.", "Fix the retry")
	mustCLI(t, base, "task", "add", "-s", "home", "--project", proj, "-p", "Say how retries work.", "Write the retry docs")
	mustCLI(t, base, "task", "add", "-s", "home", "--project", proj, "--ask-first", "Ship the release")
	mustCLI(t, base, "task", "dispatch", "cap", "2")
	term := attachIn(t, base, "home", startOpts{cols: 160, rows: 45, shippedLooks: true})

	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the agents view", "b board")
	if err := term.SendKeys("b"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the board's dispatch", "Dispatch Manual")
	for _, mode := range []string{"Dispatch Pull", "Dispatch Auto"} {
		if err := term.SendKeys(tuitest.Ctrl('t')); err != nil {
			t.Fatal(err)
		}
		waitText(t, term, "the board in "+mode, mode)
	}
	cards, raw := waitAutoCards(t, base, "home", "auto did not start agents for t1 and t2",
		func(c map[string]autoCard) bool { return held(c, "t1") && held(c, "t2") })
	if !cards["t1"].Dispatched || !cards["t2"].Dispatched || held(cards, "t3") {
		t.Fatalf("ASSERTION: want t1 and t2 held by dispatch and t3 free:\n%s", raw)
	}
	waitText(t, term, "the header's run count", "2 running")
	if text := term.Snapshot(); strings.Contains(text, "waiting for a free machine") {
		t.Fatalf("ASSERTION: a card waits, so the header changed for another reason:\n%s", text)
	}
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "board-auto-counts-160x45")
}
