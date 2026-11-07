package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
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
// that goes to done finishes its card and is given the next. After Pause, a
// finish gives no card. A usage limit stops all dispatch and puts the
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
	if stage["t1"] != "done" || stage["t2"] != "done" {
		t.Fatalf("ASSERTION: t1 and t2 are %s and %s, want done", stage["t1"], stage["t2"])
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

	// An agent that goes to done finishes its card and is given the next.
	mustCLI(t, base, "set-agent-state", "-s", sess, "-w", a, "done", "--harness", "claude-code", "-m", "finished")
	_, _, raw = waitForCards(t, base, sess, "agent a's done state did not finish "+aCard+" and give it t7",
		func(h, s map[string]string) bool { return s[aCard] == "done" && h["t7"] == a })
	fmt.Fprintf(&report, "after agent a went to done:\n%s\n", raw)

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
