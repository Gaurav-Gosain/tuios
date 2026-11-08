package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// dispatchListing is tuios task ls --json with the fields this test reads.
type dispatchListing struct {
	Tasks []struct {
		ID         string `json:"id"`
		Stage      string `json:"stage"`
		Dispatched bool   `json:"dispatched"`
		Holder     *struct {
			Window string `json:"window"`
		} `json:"holder"`
	} `json:"tasks"`
}

// cardsOf is the session's cards by id: the holder's window, "" for none,
// and the stage.
func cardsOf(t *testing.T, base, session string) (holder, stage map[string]string, raw string) {
	t.Helper()
	raw = mustCLI(t, base, "task", "ls", "-s", session, "--json")
	var l dispatchListing
	if err := json.Unmarshal([]byte(raw), &l); err != nil {
		t.Fatalf("task ls --json: %v\n%s", err, raw)
	}
	holder, stage = map[string]string{}, map[string]string{}
	for _, tk := range l.Tasks {
		stage[tk.ID] = tk.Stage
		if tk.Holder != nil {
			holder[tk.ID] = tk.Holder.Window
		}
	}
	return holder, stage, raw
}

// waitForCards polls the cards until ok says they are right, and returns the
// last listing.
func waitForCards(t *testing.T, base, session, why string, ok func(holder, stage map[string]string) bool) (map[string]string, map[string]string, string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		holder, stage, raw := cardsOf(t, base, session)
		if ok(holder, stage) {
			return holder, stage, raw
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: %s\n%s", why, raw)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestPullDispatchGivesTheNextCard: a session's board in pull mode, and two
// fake agents (panes whose agent state the test sets) that each hold a card.
// Both finish at the same moment from inside their panes with tuios task done.
// Each is given a different next card, as mail from tuios, and the card
// marked ask-first and the card for another harness are skipped. An agent
// whose turn ends keeps its card; its own task done hands the card on to
// review and gives it the next. After Pause, a finish gives no card. A usage limit stops all dispatch and puts the
// agent's card back.
//
// How it could pass wrongly: the finishes could run one after the other, so
// both panes wait on one gate; the cards could be claimed by someone else, so
// the holders are the two panes and the cards say dispatched; a pause could
// look like it held because nothing ran, so the test waits for the daemon's
// own line that says the offer was held for the pause.
//
// Negative controls (NEGATIVE_CONTROLS.md): with the pick made outside the
// state change, the two finishes can take one card; with ask_first not
// checked, t3 is taken; with the pause gate removed, t8 is taken.
func TestPullDispatchGivesTheNextCard(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "e2e-pull"
	mustCLI(t, base, "new", sess, "--detach")
	waitForSessionInfo(t, base, sess)

	a := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", sess, "agent-a", "--no-focus", "--print-id"))
	b := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", sess, "agent-b", "--no-focus", "--print-id"))
	for _, w := range []string{a, b} {
		mustCLI(t, base, "set-agent-state", "-s", sess, "-w", w, "working", "--harness", "claude-code", "-m", "on it")
	}
	add := func(args ...string) {
		mustCLI(t, base, append([]string{"task", "add", "-s", sess}, args...)...)
	}
	add("-p", "first prompt for a", "first a")                 // t1
	add("-p", "first prompt for b", "first b")                 // t2
	add("--ask-first", "-p", "needs a person", "ask me first") // t3
	add("--harness", "codex", "-p", "codex work", "for codex") // t4
	add("-p", "the fifth prompt", "next one")                  // t5
	add("-p", "the sixth prompt", "next two")                  // t6
	add("-p", "the seventh prompt", "later")                   // t7

	var report strings.Builder
	fmt.Fprintln(&report, mustCLI(t, base, "task", "dispatch", "mode", "pull", "-s", sess))
	mustCLI(t, base, "task", "claim", "t1", "-s", sess, "-w", a)
	mustCLI(t, base, "task", "claim", "t2", "-s", sess, "-w", b)

	// Both agents finish at the same moment, each from its own pane.
	gate := filepath.Join(base, "go")
	outs := map[string]string{}
	for w, card := range map[string]string{a: "t1", b: "t2"} {
		out := filepath.Join(base, card+".out")
		outs[w] = out
		script := "while [ ! -e " + gate + " ]; do sleep 0.05; done; " +
			tuiosBin + " task done " + card + " > " + out + ".tmp 2>&1; echo EXIT=$? >> " + out + ".tmp; mv " + out + ".tmp " + out + "\n"
		mustCLI(t, base, "send-text", "-s", sess, "-w", w, script)
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for w, out := range outs {
		got := waitForFileText(t, out, func(s string) bool { return strings.Contains(s, "EXIT=") }, "the pane never finished its card")
		if !strings.Contains(got, "EXIT=0") {
			t.Fatalf("ASSERTION: tuios task done in pane %s failed:\n%s", w, got)
		}
	}
	holder, stage, raw := waitForCards(t, base, sess, "the two agents were not each given one of t5 and t6",
		func(h, _ map[string]string) bool { return h["t5"] != "" && h["t6"] != "" })
	fmt.Fprintf(&report, "after both finished at once:\n%s\n", raw)
	if holder["t5"] == holder["t6"] || (holder["t5"] != a && holder["t5"] != b) || (holder["t6"] != a && holder["t6"] != b) {
		t.Fatalf("ASSERTION: t5 is held by %s and t6 by %s, want one each for %s and %s", holder["t5"], holder["t6"], a, b)
	}
	if holder["t3"] != "" || holder["t4"] != "" || holder["t7"] != "" {
		t.Fatalf("ASSERTION: dispatch took t3 (ask first), t4 (codex) or t7: %v", holder)
	}
	// An agent done with its card hands it on to review (the board has no
	// verify command); the person closes it.
	if stage["t1"] != "review" || stage["t2"] != "review" {
		t.Fatalf("ASSERTION: t1 and t2 are %s and %s, want review", stage["t1"], stage["t2"])
	}
	aCard := "t5"
	if holder["t6"] == a {
		aCard = "t6"
	}
	bCard := map[string]string{"t5": "t6", "t6": "t5"}[aCard]
	if !strings.Contains(raw, `"dispatched": true`) && !strings.Contains(raw, `"dispatched":true`) {
		t.Errorf("ASSERTION: no card says dispatch claimed it:\n%s", raw)
	}

	// The offer reached the agent as mail from tuios, with the command that
	// finishes the card.
	mail := mustCLI(t, base, "read-agent-messages", "-s", sess, "-w", a, "--peek", "--json")
	fmt.Fprintf(&report, "agent a's mail:\n%s\n", mail)
	if !strings.Contains(mail, "tuios task done "+aCard) || !strings.Contains(mail, `"from": "tuios"`) && !strings.Contains(mail, `"from":"tuios"`) {
		t.Fatalf("ASSERTION: agent a has no mail from tuios naming %s:\n%s", aCard, mail)
	}

	// An agent whose turn ends keeps its card (TestATurnEndKeepsTheCard);
	// its own task done hands the card on and gives it the next.
	mustCLI(t, base, "set-agent-state", "-s", sess, "-w", a, "done", "--harness", "claude-code", "-m", "finished")
	if out := inPane(t, base, sess, a, "task done "+aCard); !strings.Contains(out, "EXIT=0") {
		t.Fatalf("ASSERTION: tuios task done %s in agent a's pane failed:\n%s", aCard, out)
	}
	_, _, raw = waitForCards(t, base, sess, "agent a's task done did not finish "+aCard+" and give it t7",
		func(h, s map[string]string) bool { return s[aCard] == "review" && h["t7"] == a })
	fmt.Fprintf(&report, "after agent a ran task done:\n%s\n", raw)

	// Pause: a finish gives no card.
	fmt.Fprintln(&report, mustCLI(t, base, "task", "dispatch", "pause"))
	add("-p", "added after the pause", "after pause") // t8
	mustCLI(t, base, "task", "done", bCard, "-s", sess)
	logPath := daemonLogOf(t, base)
	held := "Task dispatch held for window " + b[:min(8, len(b))] + ": dispatch is paused"
	waitForFileText(t, logPath, func(s string) bool { return strings.Contains(s, held) }, "the daemon never held b's offer for the pause")
	holder, _, raw = cardsOf(t, base, sess)
	fmt.Fprintf(&report, "after b finished while paused:\n%s\n", raw)
	if holder["t8"] != "" {
		t.Fatalf("ASSERTION: t8 was given to %s while dispatch was paused", holder["t8"])
	}

	// A usage limit stops all dispatch and puts the agent's card back.
	fmt.Fprintln(&report, mustCLI(t, base, "task", "dispatch", "resume"))
	mustCLI(t, base, "set-agent-state", "-s", sess, "-w", a, "errored", "--kind", "usage_limit", "--harness", "claude-code", "-m", "usage limit reached")
	_, stage, raw = waitForCards(t, base, sess, "t7 did not go back to todo after the usage limit",
		func(h, s map[string]string) bool { return h["t7"] == "" && s["t7"] == "todo" })
	fmt.Fprintf(&report, "after agent a reported a usage limit:\n%s\n", raw)
	status := mustCLI(t, base, "task", "dispatch")
	fmt.Fprintf(&report, "dispatch status:\n%s\n", status)
	if !strings.Contains(status, "stopped, usage limit") {
		t.Fatalf("ASSERTION: dispatch did not stop for the usage limit:\n%s", status)
	}
	if holder, _, _ := cardsOf(t, base, sess); holder["t8"] != "" {
		t.Fatalf("ASSERTION: t8 was given to %s after the usage limit stop", holder["t8"])
	}
	saveText(t, "task-pull-dispatch", report.String())
}

// TestPullDispatchStopsAfterThreeFailures: on a pull board, three dispatched
// cards in a row are let go without being finished. The board stops: the
// next finish gives no card, and the daemon says why, until resume.
//
// How it could pass wrongly: a finish that gives no card could also mean the
// board had no free card, so a free card is there, and the test waits for the
// daemon's line that names the stop.
//
// Negative control (NEGATIVE_CONTROLS.md): with noteOutcome never stopping
// the board, the fourth finish gives the free card.
func TestPullDispatchStopsAfterThreeFailures(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "e2e-pull-stop"
	mustCLI(t, base, "new", sess, "--detach")
	waitForSessionInfo(t, base, sess)
	a := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", sess, "agent-a", "--no-focus", "--print-id"))
	mustCLI(t, base, "set-agent-state", "-s", sess, "-w", a, "working", "--harness", "claude-code", "-m", "on it")
	mustCLI(t, base, "task", "dispatch", "mode", "pull", "-s", sess)
	mustCLI(t, base, "task", "add", "-s", sess, "flaky work") // t1, given and let go each round
	var report strings.Builder
	for round := 1; round <= 3; round++ {
		own := fmt.Sprintf("own %d", round)
		mustCLI(t, base, "task", "add", "-s", sess, own)
		id := fmt.Sprintf("t%d", round+1)
		mustCLI(t, base, "task", "claim", id, "-s", sess, "-w", a)
		mustCLI(t, base, "task", "done", id, "-s", sess)
		waitForCards(t, base, sess, fmt.Sprintf("round %d: t1 was not given to agent a", round),
			func(h, _ map[string]string) bool { return h["t1"] == a })
		fmt.Fprintln(&report, mustCLI(t, base, "task", "release", "t1", "-s", sess))
	}
	status := mustCLI(t, base, "task", "dispatch", "-s", sess)
	fmt.Fprintf(&report, "after three failures:\n%s\n", status)
	if !strings.Contains(status, "stopped: 3 failures in a row") {
		t.Fatalf("ASSERTION: the board did not stop after three failures:\n%s", status)
	}
	mustCLI(t, base, "task", "add", "-s", sess, "own 4") // t5
	mustCLI(t, base, "task", "claim", "t5", "-s", sess, "-w", a)
	mustCLI(t, base, "task", "done", "t5", "-s", sess)
	held := "Task dispatch held for window " + a[:min(8, len(a))] + ": the board stopped: 3 failures in a row"
	waitForFileText(t, daemonLogOf(t, base), func(s string) bool { return strings.Contains(s, held) }, "the daemon never held the offer for the stop")
	if holder, _, raw := cardsOf(t, base, sess); holder["t1"] != "" {
		t.Fatalf("ASSERTION: t1 was given to %s on a stopped board:\n%s", holder["t1"], raw)
	}
	fmt.Fprintln(&report, mustCLI(t, base, "task", "dispatch", "resume"))
	saveText(t, "task-pull-dispatch-stop", report.String())
}

// TestATurnEndKeepsTheCard: an agent on a pull board, and one on an auto
// board, each hold a card, one of them claimed by hand. Each ends its turn
// with a question, through the Claude Code Stop hook every turn end runs.
// The cards stay in doing with their holders, and the pull agent is given no
// next card. Only the agent's own `tuios task done` hands its card in, and
// that gives it the next one.
//
// How it could pass wrongly: a Stop the daemon never read would also leave
// the cards alone, so the test first waits until each pane reads done with
// the question, and the positive half shows the same pane is given the next
// card once it runs task done.
//
// Negative controls (NEGATIVE_CONTROLS.md, TestATurnEndKeepsTheCard).
func TestATurnEndKeepsTheCard(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	t.Cleanup(func() { dumpDaemonLogOnFail(t, base) })
	const pull, auto = "e2e-turn-pull", "e2e-turn-auto"
	var report strings.Builder
	note := func(format string, args ...any) { fmt.Fprintf(&report, format+"\n", args...) }
	defer func() { saveText(t, "task-turn-end", report.String()) }()
	const question = "Should I use the retry library or write my own?"

	stop := func(sess, w string) {
		t.Helper()
		cmd := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", sess, "--window", w, "--timeout", "10s")
		cmd.Dir = workDirIn(t, base)
		cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
		for _, key := range xdgKeys {
			cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
		}
		cmd.Stdin = strings.NewReader(`{"hook_event_name":"Stop","session_id":"turn-` + sess + `","last_assistant_message":"` + question + `"}`)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("the Stop hook failed: %v\n%s", err, out)
		}
		deadline := time.Now().Add(uiTimeout)
		for {
			out := mustCLI(t, base, "list-agents", "-s", sess, "--json")
			if strings.Contains(out, `"done"`) && strings.Contains(out, "retry library") {
				note("agents of %s after the Stop:\n%s", sess, out)
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("ASSERTION: the pane never read done with the question after the Stop hook:\n%s", out)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	held := func(sess, card, w string) {
		t.Helper()
		// Dispatch reads the state off the sink: give it time to act.
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			holder, stage, raw := cardsOf(t, base, sess)
			if holder[card] != w || stage[card] != "doing" {
				t.Fatalf("ASSERTION: after a turn end with a question, %s is %s held by %q, want doing held by %s:\n%s", card, stage[card], holder[card], w, raw)
			}
			time.Sleep(200 * time.Millisecond)
		}
		_, _, raw := cardsOf(t, base, sess)
		note("cards of %s after the turn end:\n%s", sess, raw)
	}

	// Pull: t1 dispatched by a finish, t2 waits as the next card.
	mustCLI(t, base, "new", pull, "--detach")
	waitForSessionInfo(t, base, pull)
	a := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", pull, "agent-a", "--no-focus", "--print-id"))
	mustCLI(t, base, "set-agent-state", "-s", pull, "-w", a, "working", "--harness", "claude-code", "-m", "on it")
	mustCLI(t, base, "task", "add", "-s", pull, "-p", "warm up", "warm up")     // t1
	mustCLI(t, base, "task", "add", "-s", pull, "-p", "step one", "first card") // t2
	mustCLI(t, base, "task", "add", "-s", pull, "-p", "step two", "next card")  // t3
	note("%s", mustCLI(t, base, "task", "dispatch", "mode", "pull", "-s", pull))
	mustCLI(t, base, "task", "claim", "t1", "-s", pull, "-w", a)
	if out := inPane(t, base, pull, a, "task done t1"); !strings.Contains(out, "EXIT=0") {
		t.Fatalf("ASSERTION: tuios task done t1 in the agent's pane failed:\n%s", out)
	}
	waitForCards(t, base, pull, "the pull board did not give agent a t2 after t1",
		func(h, _ map[string]string) bool { return h["t2"] == a })
	mustCLI(t, base, "set-agent-state", "-s", pull, "-w", a, "working", "--harness", "claude-code", "-m", "on t2")
	stop(pull, a)
	held(pull, "t2", a)
	if holder, _, raw := cardsOf(t, base, pull); holder["t3"] != "" {
		t.Fatalf("ASSERTION: a turn end gave agent a the next card t3:\n%s", raw)
	}

	// Auto: a card the person gave the agent by hand. Ask first keeps auto
	// from starting an agent for it while it is open.
	mustCLI(t, base, "new", auto, "--detach")
	waitForSessionInfo(t, base, auto)
	b := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", auto, "agent-b", "--no-focus", "--print-id"))
	mustCLI(t, base, "set-agent-state", "-s", auto, "-w", b, "working", "--harness", "claude-code", "-m", "on it")
	mustCLI(t, base, "task", "add", "-s", auto, "--ask-first", "-p", "by hand", "given by hand") // t1
	mustCLI(t, base, "task", "claim", "t1", "-s", auto, "-w", b)
	note("%s", mustCLI(t, base, "task", "dispatch", "mode", "auto", "--agent", "claude", "-s", auto))
	stop(auto, b)
	held(auto, "t1", b)

	// The agent's own task done hands the card in and pulls the next.
	if out := inPane(t, base, pull, a, "task done t2"); !strings.Contains(out, "EXIT=0") {
		t.Fatalf("ASSERTION: tuios task done t2 in the agent's pane failed:\n%s", out)
	}
	_, _, raw := waitForCards(t, base, pull, "agent a's task done did not hand in t2 and give it t3",
		func(h, s map[string]string) bool { return s["t2"] == "review" && h["t3"] == a })
	note("after agent a ran task done t2:\n%s", raw)
}

// TestPullDispatchOffersAgainWhenTheSwitchesLift: on a pull board, an offer
// that a switch held is made when the switch lifts. An agent at rest with no
// card is offered one when the person sets the board to pull. A finish while
// dispatch is paused is offered its next card on resume, and a finish in
// quiet hours when the quiet hours end. A card that comes free, added or let
// go, is offered to an agent at rest, never by such an offer to the pane that let it go.
//
// How it could pass wrongly: the next card could come from the finish itself,
// so each held offer is first seen held (the daemon's line that says why, and
// no new holder), and only then is the switch lifted.
//
// Negative controls (NEGATIVE_CONTROLS.md, TestPullDispatchOffersAgainWhenTheSwitchesLift).
func TestPullDispatchOffersAgainWhenTheSwitchesLift(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	t.Cleanup(func() { dumpDaemonLogOnFail(t, base) })
	const sess = "e2e-pull-again"
	mustCLI(t, base, "new", sess, "--detach")
	waitForSessionInfo(t, base, sess)
	var report strings.Builder
	note := func(format string, args ...any) { fmt.Fprintf(&report, format+"\n", args...) }
	defer func() { saveText(t, "task-pull-again", report.String()) }()

	a := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", sess, "agent-a", "--no-focus", "--print-id"))
	b := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", sess, "agent-b", "--no-focus", "--print-id"))
	rest := func(w string) {
		mustCLI(t, base, "set-agent-state", "-s", sess, "-w", w, "done", "--harness", "claude-code", "-m", "ready")
	}
	mustCLI(t, base, "set-agent-state", "-s", sess, "-w", a, "working", "--harness", "claude-code", "-m", "on it")
	rest(b)
	for i := 1; i <= 5; i++ {
		mustCLI(t, base, "task", "add", "-s", sess, "-p", fmt.Sprintf("step %d", i), fmt.Sprintf("card %d", i))
	}
	mustCLI(t, base, "task", "claim", "t1", "-s", sess, "-w", a)
	logPath := daemonLogOf(t, base)
	finish := func(w, card, why string) {
		t.Helper()
		if out := inPane(t, base, sess, w, "task done "+card); !strings.Contains(out, "EXIT=0") {
			t.Fatalf("ASSERTION: tuios task done %s in pane %s failed:\n%s", card, w, out)
		}
		held := "Task dispatch held for window " + w[:8] + ": " + why
		waitForFileText(t, logPath, func(s string) bool { return strings.Contains(s, held) }, "the daemon never held the offer: "+held)
		rest(w)
		holder, _, raw := cardsOf(t, base, sess)
		note("after %s finished %s (%s):\n%s", w[:8], card, why, raw)
		for id, h := range holder {
			if h == w {
				t.Fatalf("ASSERTION: pane %s was given %s while %s", w[:8], id, why)
			}
		}
	}

	// An agent at rest with no card, when the board is set to pull.
	note("%s", mustCLI(t, base, "task", "dispatch", "mode", "pull", "-s", sess))
	_, _, raw := waitForCards(t, base, sess, "agent b, at rest with no card, was not offered t2 when the board was set to pull",
		func(h, _ map[string]string) bool { return h["t2"] == b })
	note("after the board was set to pull:\n%s", raw)

	// A finish while paused, then resume.
	note("%s", mustCLI(t, base, "task", "dispatch", "pause"))
	finish(a, "t1", "dispatch is paused")
	note("%s", mustCLI(t, base, "task", "dispatch", "resume"))
	_, _, raw = waitForCards(t, base, sess, "agent a's offer held by the pause was not made on resume",
		func(h, _ map[string]string) bool { return h["t3"] == a })
	note("after resume:\n%s", raw)

	// A finish in quiet hours, then the quiet hours end. The span ends at
	// the next minute.
	if time.Now().Second() > 50 {
		time.Sleep(time.Duration(61-time.Now().Second()) * time.Second)
	}
	now := time.Now()
	span := now.Add(-2*time.Minute).Format("15:04") + "-" + now.Add(time.Minute).Format("15:04")
	note("%s", mustCLI(t, base, "task", "dispatch", "quiet", span))
	finish(b, "t2", "quiet hours")
	deadline := time.Now().Add(90 * time.Second)
	for {
		holder, _, raw := cardsOf(t, base, sess)
		if holder["t4"] == b {
			note("after the quiet hours %s ended:\n%s", span, raw)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: agent b's offer held by the quiet hours %s was not made when they ended:\n%s", span, raw)
		}
		time.Sleep(500 * time.Millisecond)
	}

	// A card that comes free is offered to an agent at rest. a takes t5 on
	// its finish; b finishes with no free card left and rests.
	note("%s", mustCLI(t, base, "task", "dispatch", "quiet", "off"))
	inPane(t, base, sess, a, "task done t3")
	rest(a)
	waitForCards(t, base, sess, "agent a was not given t5 on its finish",
		func(h, _ map[string]string) bool { return h["t5"] == a })
	inPane(t, base, sess, b, "task done t4")
	rest(b)
	mustCLI(t, base, "task", "add", "-s", sess, "-p", "step 6", "card 6") // t6
	_, _, raw = waitForCards(t, base, sess, "agent b, at rest with no card, was not offered t6 when it was added",
		func(h, _ map[string]string) bool { return h["t6"] == b })
	note("after t6 was added:\n%s", raw)
	// Each lets its card go: each is offered the other's, never its own.
	inPane(t, base, sess, a, "task release t5")
	inPane(t, base, sess, b, "task release t6")
	holder, _, raw := waitForCards(t, base, sess, "the two released cards were not offered to the agents at rest",
		func(h, _ map[string]string) bool { return h["t5"] != "" && h["t6"] != "" })
	note("after a let t5 go and b let t6 go:\n%s", raw)
	if holder["t5"] != b || holder["t6"] != a {
		t.Fatalf("ASSERTION: t5 is held by %s and t6 by %s; want each agent offered the card the other let go, a=%s b=%s", holder["t5"], holder["t6"], a, b)
	}
}
