package tuie2e

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// pushPlanPrompt is a card prompt of a few KiB, as a person writes one for
// an agent.
var pushPlanPrompt = strings.Join([]string{
	"The reconnect test in the e2e suite fails about one run in twenty on a loaded machine, and only when the client",
	"attaches while the daemon is still writing the previous state push. Start by running it a hundred times under",
	"stress with the race detector on and keep every failing log. Read the attach path from the client's dial to the",
	"first frame it draws, and write down every place that waits on a timer instead of a condition. For each of them,",
	"say what the condition should be and whether the daemon already sends a message the client could wait on.",
	"Do not change the frame limit, the codec or the order of messages on the wire: the fix belongs in the test or in",
	"the client's own wait. If the race is real and not only in the test, write a failing unit test for it first and",
	"stop there, with a short note on what you found, so a person can decide how to fix it. Keep the change small.",
	"When you are done, list the runs you made, how many failed before and after, and the machine load during each.",
	"Leave the logs of the failing runs in the worktree under flake-logs so the reviewer can read them without",
	"running anything. If a run hangs, end it after five minutes and count it as a failure with the hang named.",
	"Check the same test on the other backend as well, since scrollback depth differs between them, and note any",
	"difference in how often it fails. Mention every helper you touched and why it needed touching at all.",
	"Some background on why this matters now. The reconnect path was rewritten twice this year, once to move the",
	"handshake into its own goroutine and once to let a client resume after the daemon restarts, and each rewrite",
	"left the test a little more dependent on timing than it was before. Nobody has looked at it as a whole since.",
	"The flake costs every contributor a rerun now and then, and twice it hid a real regression for a day because",
	"people had learned to ignore it. So the goal is not only a green run but a test whose failure means something.",
	"If you find that the test checks something no user would notice, say so; deleting a test is a valid outcome.",
	"If you find two separate problems, report them separately and fix only the one in the test. Ask before you",
	"touch anything in the daemon, and do not reformat files you only read. Use the existing helpers for waiting on",
	"the screen and on the daemon's listings rather than writing new ones, unless none of them fits, and if you do",
	"write one, put it next to the test that needs it and say in its comment what it waits for and why.",
	"Finally, time the test before and after on the same machine with nothing else running, fastest of three runs,",
	"and put the numbers in your note. A fix that makes the test much slower is worth knowing about before review.",
}, " ")

// pushCardDetail is what tuios task get --json says of a card.
type pushCardDetail struct {
	Prompt  string            `json:"prompt"`
	History []json.RawMessage `json:"history"`
	Verify  *struct {
		State string `json:"state"`
		Tail  string `json:"tail"`
	} `json:"verify"`
}

// pushTaskGet reads card id of session with tuios task get --json.
func pushTaskGet(t *testing.T, base, session, id string) (pushCardDetail, string) {
	t.Helper()
	raw := mustCLI(t, base, "task", "get", id, "-s", session, "--json")
	var d pushCardDetail
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatalf("task get %s --json: %v\n%s", id, err, raw)
	}
	return d, raw
}

// TestTaskCardsKeepTheirBodiesThroughStatePushes: a session holds two cards,
// one with a prompt of a few KiB and one whose verify failed with output. A
// client is attached, and the session changes while it is: a pane is
// renamed and the long-prompt card moves from todo to doing, each pushing
// the state to the client. The daemon still gives the whole prompt, the
// history and the verify output with tuios task get, and the client's board
// shows both cards.
//
// How it could pass wrongly: the verify card is read only after the daemon
// says its verify failed, so a verify that never ran fails there; each
// change is waited on in the daemon's own listing before the cards are read.
//
// Negative control (NEGATIVE_CONTROLS.md): with the push clearing the verify
// output through the card's shared record, task get returns an empty tail.
func TestTaskCardsKeepTheirBodiesThroughStatePushes(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	t.Cleanup(func() { dumpDaemonLogOnFail(t, base) })
	const sess = "e2e-push"
	mustCLI(t, base, "new", sess, "--detach")
	waitForSessionInfo(t, base, sess)

	// An agent pane in a git repository, as the verify command needs.
	repo := filepath.Join(base, "demo")
	lifeRepo(t, repo)
	a := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", sess, "agent-a", "--no-focus", "--print-id", "--cwd", repo))
	mustCLI(t, base, "set-agent-state", "-s", sess, "-w", a, "working", "--harness", "codex", "-m", "on it")

	// The board's verify command first, then the card its agent is done
	// with: the verify fails and the card goes back to todo with the output.
	mustCLI(t, base, "task", "dispatch", "verify", "-s", sess, `test -f ok || { echo "first line"; echo "not ok yet"; exit 3; }`)
	const verifyTitle, planTitle = "fix the flaky reconnect test", "plan the reconnect rework"
	mustCLI(t, base, "task", "add", "-s", sess, "-p", "make the reconnect test pass", verifyTitle) // t1
	mustCLI(t, base, "task", "claim", "t1", "-s", sess, "-w", a)
	if out := inPane(t, base, sess, a, "task done t1"); !strings.Contains(out, "EXIT=0") {
		t.Fatalf("ASSERTION: tuios task done t1 in the agent's pane failed:\n%s", out)
	}
	waitLife(t, base, sess, uiTimeout, "t1 did not go back to todo when its verify failed",
		func(c map[string]lifeCard) bool {
			return c["t1"].Stage == "todo" && c["t1"].Verify != nil && c["t1"].Verify.State == "failed"
		})
	mustCLI(t, base, "task", "add", "-s", sess, "-p", pushPlanPrompt, planTitle) // t2

	// A client attached, and the session changes under it.
	term := attachIn(t, base, sess, startOpts{cols: 200, rows: 55, shippedLooks: true})
	mustCLI(t, base, "set-window", "-s", sess, "--name", "renamed-shell")
	for deadline := time.Now().Add(uiTimeout); ; time.Sleep(100 * time.Millisecond) {
		out := mustCLI(t, base, "list-windows", "-s", sess)
		if strings.Contains(out, "renamed-shell") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the rename never reached the session's windows:\n%s", out)
		}
	}
	mustCLI(t, base, "task", "stage", "t2", "doing", "-s", sess)
	waitLife(t, base, sess, uiTimeout, "t2 did not move to doing",
		func(c map[string]lifeCard) bool { return c["t2"].Stage == "doing" })

	// The daemon still holds every body.
	plan, raw := pushTaskGet(t, base, sess, "t2")
	if plan.Prompt != pushPlanPrompt || len(plan.History) == 0 {
		t.Errorf("ASSERTION: after the pushes t2 has a %d-byte prompt (want the %d bytes it was given) and %d history entries:\n%s",
			len(plan.Prompt), len(pushPlanPrompt), len(plan.History), raw)
	}
	failed, raw := pushTaskGet(t, base, sess, "t1")
	if failed.Prompt != "make the reconnect test pass" || len(failed.History) == 0 || failed.Verify == nil ||
		failed.Verify.State != "failed" || !strings.Contains(failed.Verify.Tail, "first line") || !strings.Contains(failed.Verify.Tail, "not ok yet") {
		t.Errorf("ASSERTION: after the pushes t1 lost its prompt, its history or its verify output:\n%s", raw)
	}

	// The client's board shows both cards. Neither is held, so neither title
	// is drawn in the agents view before b.
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	waitText(t, term, "the agents view", "Working  1")
	if err := term.SendKeys("b"); err != nil {
		t.Fatalf("press b: %v", err)
	}
	waitText(t, term, "the board with both cards", verifyTitle, planTitle)
}
