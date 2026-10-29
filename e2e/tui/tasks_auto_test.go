package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The cards that move on their own (internal/session/task_auto.go), through
// the binary a person runs. Each test leaves the history of the cards it
// moved as its artifact.

// taskFanRow is one card of tuios task ls --json, with its fan.
type taskFanRow struct {
	ID     string `json:"id"`
	Stage  string `json:"stage"`
	Holder *struct {
		Window string `json:"window"`
	} `json:"holder"`
	Fan *struct {
		Fan   string `json:"fan"`
		FanID string `json:"fan_id"`
	} `json:"fan"`
}

func taskFanRows(t *testing.T, base, session string) map[string]taskFanRow {
	t.Helper()
	var l struct {
		Tasks []taskFanRow `json:"tasks"`
	}
	out := mustCLI(t, base, "task", "ls", "-s", session, "--json")
	if err := json.Unmarshal([]byte(out), &l); err != nil {
		t.Fatalf("task ls --json: %v\n%s", err, out)
	}
	rows := map[string]taskFanRow{}
	for _, r := range l.Tasks {
		rows[r.ID] = r
	}
	return rows
}

// stageLine is how tuios task get prints a stage.
func stageLine(stage string) string { return "stage   " + stage + "\n" }

// stillIn reads the card right after a move that settled it, and fails when
// it is not in stage.
func stillIn(t *testing.T, base, session, id, stage, why string) string {
	t.Helper()
	got := mustCLI(t, base, "task", "get", "-s", session, id)
	if !strings.Contains(got, stageLine(stage)) {
		t.Fatalf("ASSERTION: %s left %s, though %s:\n%s", id, stage, why, got)
	}
	return got
}

// planHookPayload is a Claude Code plan held for the person.
const planHookPayload = `{"hook_event_name":"PermissionRequest","session_id":"e2e-plan","permission_mode":"plan","tool_name":"ExitPlanMode","tool_input":{"plan":"# Retry\n1. Add backoff.","planFilePath":"/tmp/plan.md"}}`

// answerPlan waits for the held plan in the open Inbox, with the hold known
// so the footer offers the answers, presses key once the Inbox takes an
// answer, and waits for the hook to return with it.
func answerPlan(t *testing.T, base string, term *tuitest.Terminal, hook *heldHook, key, want string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool { return screenHas(s, "Approve the plan?", "a approve", "r keep planning") }, uiTimeout); err != nil {
		t.Fatalf("the Inbox never showed the plan: %v\n%s", err, term.Snapshot())
	}
	// The Inbox takes no answer until the item has been shown a moment.
	time.Sleep(answerSettle)
	if err := term.SendKeys(key); err != nil {
		t.Fatalf("answer the plan: %v", err)
	}
	select {
	case err := <-hook.exited:
		if err != nil {
			t.Fatalf("the hook failed: %v", err)
		}
	case <-time.After(uiTimeout):
		t.Fatalf("the hook never returned after %s\n%s\n%s", key, term.Snapshot(), daemonLogTail(base, 40))
	}
	if out := hook.stdout.String(); !strings.Contains(out, want) {
		t.Fatalf("the hook printed %q, want %q", out, want)
	}
}

// TestTaskFollowsAPlanHoldAndItsAnswer: a pane holds three cards. When its
// agent's plan is held for the person, the card in doing moves to plan, the
// card set to review by hand stays in review, and the blocked card stays in
// doing. Kept planning from the Inbox, the planned card stays in plan. A
// second plan approved from the Inbox moves it to doing. Each move's history
// entry is by tuios and names why, and a plan kept planning leaves none.
//
// How it could pass wrongly: a card claimed in doing that never moved would
// read doing at the end too, so the card must be seen in plan first, with the
// plan's reason in its history, and the approval's reason after. The three
// cards are read right after t1 reached plan, since one state change decided
// them all; the kept plan is read once the hook has its answer, which comes
// after the reply the move would ride.
//
// Negative controls: with the plan hold not moving cards (the autoStage call
// in verbRequestApproval), t1 never reaches plan. With the answer not moving
// cards (notePlanAnswered), t1 stays in plan after the approval. With applies
// ignoring a block, t3 moves to plan.
func TestTaskFollowsAPlanHoldAndItsAnswer(t *testing.T) {
	base, term := approvalsClient(t)
	const sess = "e2e-agent"
	pane := windowIDs(t, base, sess)[0]
	mustCLI(t, base, "task", "add", "-s", sess, "plan the retry")
	mustCLI(t, base, "task", "add", "-s", sess, "--stage", "review", "already in review")
	mustCLI(t, base, "task", "add", "-s", sess, "waiting on a key")
	for _, id := range []string{"t1", "t2", "t3"} {
		mustCLI(t, base, "task", "claim", "-s", sess, id, "-w", pane)
	}
	mustCLI(t, base, "task", "block", "-s", sess, "t3", "the staging key")

	hook := startHeldHook(t, base, planHookPayload)
	var report strings.Builder
	planned := waitForTaskText(t, base, sess, "t1", stageLine("plan"), "staged    by tuios: plan held for approval")
	fmt.Fprintf(&report, "t1 with the plan held:\n%s\n\n", planned)
	fmt.Fprintf(&report, "t2 with the plan held:\n%s\n\n", stillIn(t, base, sess, "t2", "review", "it was put in review by hand"))
	fmt.Fprintf(&report, "t3 with the plan held:\n%s\n\n", stillIn(t, base, sess, "t3", "doing", "it is blocked"))

	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	answerPlan(t, base, term, hook, "r", `"behavior":"deny"`)
	kept := stillIn(t, base, sess, "t1", "plan", "the plan was kept planning")
	if strings.Contains(kept, "plan approved") {
		t.Fatalf("ASSERTION: t1 reads an approval after a plan kept planning:\n%s", kept)
	}
	fmt.Fprintf(&report, "t1 with the plan kept planning:\n%s\n\n", kept)

	hook = startHeldHook(t, base, planHookPayload)
	answerPlan(t, base, term, hook, "a", `"behavior":"allow"`)
	fmt.Fprintf(&report, "t1 with the plan approved:\n%s\n", waitForTaskText(t, base, sess, "t1", stageLine("doing"), "staged    by tuios: plan approved"))
	saveText(t, "tasks-auto-plan", report.String())
	alive(t, term, "after a plan kept planning and one approved")
}

// TestTaskFollowsItsFan: fan --task stamps a card of the conductor's session
// with the fan, moves it to doing, and leaves it unheld, and claim-task next
// takes the card after it instead. A fan check that fails in both attempts
// moves the card to verify and back to doing. A check that passes in one
// leaves it in verify, read once fan verify has returned, which it does when
// every check has ended.
//
// How it could pass wrongly: the stamp could be read from a card added inside
// a fan session, so the card lives in a session of no fan; next could skip it
// for being in doing, so the card after it is in todo and would be taken
// first only by a claim that skips the stamped card.
//
// Negative controls: with nextFreeTask not skipping a card a live fan works
// on, next takes t1. With noteFanVerifyEnded doing nothing, t1 stays in
// verify after the failed check.
func TestTaskFollowsItsFan(t *testing.T) {
	base, repo := fanFixture(t)
	const conductor = "conductor"
	mustCLI(t, base, "new", conductor, "--detach")
	waitForSessionInfo(t, base, conductor)
	mustCLI(t, base, "task", "add", "-s", conductor, "add a retry")
	mustCLI(t, base, "task", "add", "-s", conductor, "add a timeout")

	out := mustCLI(t, base, "fan", "2", "--agent", "claude", "--repo", repo, "--name", "try/task", "--task", "t1", "--task-session", conductor, "Add a retry.")
	if !strings.Contains(out, "Task t1 follows the fan.") {
		t.Fatalf("ASSERTION: fan --task did not say it stamped t1:\n%s", out)
	}
	rows := taskFanRows(t, base, conductor)
	if r := rows["t1"]; r.Fan == nil || r.Fan.Fan != "try/task" || r.Stage != "doing" || r.Holder != nil {
		t.Fatalf("ASSERTION: t1 after fan --task = %+v, want doing in fan try/task with no holder", r)
	}
	worker := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", conductor, "worker", "--no-focus", "--print-id"))
	if got := strings.TrimSpace(mustCLI(t, base, "task", "claim", "-s", conductor, "--next", "-w", worker)); !strings.HasPrefix(got, "t2") {
		t.Fatalf("ASSERTION: claim --next took %q, want t2: t1 is the fan's", got)
	}

	var report strings.Builder
	if out, err := tuiosCLI(t, base, "fan", "verify", "repo-try-task", "--", "false"); err == nil {
		t.Fatalf("fan verify exited 0 with a check that fails:\n%s", out)
	}
	failed := waitForTaskText(t, base, conductor, "t1", stageLine("doing"), "by tuios: fan check started", "by tuios: fan check failed in every attempt")
	fmt.Fprintf(&report, "t1 after a check that failed everywhere:\n%s\n\n", failed)

	if out, err := tuiosCLI(t, base, "fan", "verify", "repo-try-task", "--", "test", "-f", "done.txt"); err == nil {
		t.Fatalf("fan verify exited 0 before any attempt passed:\n%s", out)
	}
	var winner string
	for _, r := range worktreeRows(t, base, "--group", "try/task") {
		if r["session"] == "repo-try-task-2" {
			winner = r["path"].(string)
		}
	}
	if err := os.WriteFile(filepath.Join(winner, "done.txt"), []byte("done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForTaskText(t, base, conductor, "t1", stageLine("doing"))
	out, _ = tuiosCLI(t, base, "fan", "verify", "repo-try-task", "--", "test", "-f", "done.txt")
	fmt.Fprintf(&report, "fan verify with one attempt passing:\n%s\n\n", out)
	if !strings.Contains(out, "1 of 2 passed.") {
		t.Fatalf("the checks did not end as one pass and one fail:\n%s", out)
	}
	fmt.Fprintf(&report, "t1 after a check that passed in one attempt:\n%s\n", stillIn(t, base, conductor, "t1", "verify", "one attempt passed"))
	saveText(t, "tasks-auto-fan", report.String())
}

// TestTaskFollowsItsPullRequest: a card held by a pane of a worktree session
// moves to review when the branch's pull request is found open, to shipped
// when the poll reads it merged, and stays there. Put back in review by hand,
// it goes to doing when the pull request reads closed without a merge. The
// card starts in plan, as one does after a hold that timed out and a plan
// approved in the pane, since the pull request is proof the plan went
// through; a second card parked in plan while the pull request is open goes
// to shipped with the merge. A blocked card held there, and one put in todo
// by hand, stay where they are through all of it.
//
// How it could pass wrongly: the poll could never run, and shipped come from
// another path, so the merge is written only after the card is in review and
// read only by the poll; the history must name the pull request. The cards
// that stay are read right after t1 moved, since one state change decided
// them all.
//
// Negative controls: with notePR not moving cards, t1 stays in plan after
// the pull request is found. With plan left out of the pull request moves, t1
// stays in plan too. With applies ignoring a block, t2 moves to review.
func TestTaskFollowsItsPullRequest(t *testing.T) {
	f := newShipFixture(t)
	base := f.base
	pane := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", f.session, "worker", "--no-focus", "--print-id"))
	mustCLI(t, base, "task", "add", "-s", f.session, "ship the note")
	mustCLI(t, base, "task", "add", "-s", f.session, "waiting on a key")
	mustCLI(t, base, "task", "add", "-s", f.session, "put back by hand")
	for _, id := range []string{"t1", "t2", "t3"} {
		mustCLI(t, base, "task", "claim", "-s", f.session, id, "-w", pane)
	}
	mustCLI(t, base, "task", "block", "-s", f.session, "t2", "the staging key")
	mustCLI(t, base, "task", "stage", "-s", f.session, "t3", "todo")
	mustCLI(t, base, "task", "stage", "-s", f.session, "t1", "plan")

	pr := func(state string) {
		t.Helper()
		body := `{"number":7,"url":"https://github.example/o/r/pull/7","state":"` + state + `","statusCheckRollup":[]}`
		if err := os.WriteFile(filepath.Join(f.fakes, "pr.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	others := func(report *strings.Builder, when string) {
		t.Helper()
		fmt.Fprintf(report, "t2 %s:\n%s\n\n", when, stillIn(t, base, f.session, "t2", "doing", "it is blocked"))
		fmt.Fprintf(report, "t3 %s:\n%s\n\n", when, stillIn(t, base, f.session, "t3", "todo", "it was put in todo by hand"))
	}
	var report strings.Builder
	pr("OPEN")
	mustCLI(t, base, "ship", "status", "-s", f.session, "--refresh")
	fmt.Fprintf(&report, "t1 with the pull request open:\n%s\n\n", waitForTaskText(t, base, f.session, "t1", stageLine("review"), "by tuios: pull request #7 opened"))
	others(&report, "with the pull request open")

	// The poll runs while a client is attached.
	term := startIn(t, base, startOpts{args: []string{"attach", f.session}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	mustCLI(t, base, "task", "add", "-s", f.session, "planned while the pull request was open")
	mustCLI(t, base, "task", "claim", "-s", f.session, "t4", "-w", pane)
	mustCLI(t, base, "task", "stage", "-s", f.session, "t4", "plan")
	pr("MERGED")
	fmt.Fprintf(&report, "t1 once the poll read it merged:\n%s\n\n", waitForTaskText(t, base, f.session, "t1", stageLine("shipped"), "by tuios: pull request #7 merged"))
	fmt.Fprintf(&report, "t4, in plan, with the pull request merged:\n%s\n\n", stillIn(t, base, f.session, "t4", "shipped", "a merge moves a card out of plan"))
	others(&report, "with the pull request merged")

	mustCLI(t, base, "task", "stage", "-s", f.session, "t1", "review")
	pr("CLOSED")
	mustCLI(t, base, "ship", "status", "-s", f.session, "--refresh")
	fmt.Fprintf(&report, "t1 with the pull request closed:\n%s\n", waitForTaskText(t, base, f.session, "t1", stageLine("doing"), "by tuios: pull request #7 closed without a merge"))
	saveText(t, "tasks-auto-pr", report.String())
}

// TestTaskAutoStageCanBeTurnedOff: with [tasks] auto_stage = false, a card
// held by a pane whose plan is held and then approved stays in doing, with
// no entry by tuios in its history, and start-agent --task still claims the
// card for the pane it opens.
//
// How it could pass wrongly: the card could be read before the hold moved
// it, so it is read once the hook has its answer, which comes after both
// moves would have been made; the history is checked, not only the stage.
//
// Negative control: with the daemon not reading [tasks] auto_stage
// (DaemonConfigFromUser), t1's history gains entries by tuios.
func TestTaskAutoStageCanBeTurnedOff(t *testing.T) {
	base, term := approvalsClientWith(t, "[tasks]\nauto_stage = false\n")
	const sess = "e2e-agent"
	pane := windowIDs(t, base, sess)[0]
	mustCLI(t, base, "task", "add", "-s", sess, "stays put")
	mustCLI(t, base, "task", "claim", "-s", sess, "t1", "-w", pane)

	hook := startHeldHook(t, base, planHookPayload)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	answerPlan(t, base, term, hook, "a", `"behavior":"allow"`)
	var report strings.Builder
	got := stillIn(t, base, sess, "t1", "doing", "auto_stage is off")
	if strings.Contains(got, "by tuios") {
		t.Fatalf("ASSERTION: t1 moved on its own, though auto_stage is off:\n%s", got)
	}
	fmt.Fprintf(&report, "t1 with auto_stage off, its plan held and approved:\n%s\n\n", got)

	mustCLI(t, base, "task", "add", "-s", sess, "for a new agent")
	out, _ := tuiosCLI(t, base, "start-agent", "sh", "-s", sess, "--name", "t2-worker", "--task", "t2", "--ready-timeout", "500")
	fmt.Fprintf(&report, "start-agent --task t2:\n%s\n", out)
	if !strings.Contains(out, "holds task t2") {
		t.Fatalf("ASSERTION: start-agent --task did not say the pane holds t2:\n%s", out)
	}
	rows := taskFanRows(t, base, sess)
	if r := rows["t2"]; r.Holder == nil || r.Holder.Window != windowIDByName(t, base, sess, "t2-worker") || r.Stage != "doing" {
		t.Fatalf("ASSERTION: t2 after start-agent --task = %+v, want doing, held by t2-worker", r)
	}
	saveText(t, "tasks-auto-off", report.String())
	alive(t, term, "after a plan with auto_stage off")
}
