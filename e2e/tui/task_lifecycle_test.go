package tuie2e

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// lifeCard is a card of tuios task ls --json with the lifecycle fields.
type lifeCard struct {
	ID       string `json:"id"`
	Stage    string `json:"stage"`
	Attempts int    `json:"attempts"`
	Note     string `json:"note"`
	Holder   *struct {
		Window string `json:"window"`
		Host   string `json:"host"`
	} `json:"holder"`
	Work *struct {
		Host   string `json:"host"`
		Window string `json:"window"`
		Dir    string `json:"dir"`
	} `json:"work"`
	Verify *struct {
		State string `json:"state"`
		Exit  *int   `json:"exit"`
		Note  string `json:"note"`
		Tail  string `json:"tail"`
	} `json:"verify"`
}

// lifeCards is the session's cards by id, and the raw listing.
func lifeCards(t *testing.T, base, session string) (map[string]lifeCard, string) {
	t.Helper()
	raw := mustCLI(t, base, "task", "ls", "-s", session, "--json")
	var l struct {
		Tasks []lifeCard `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(raw), &l); err != nil {
		t.Fatalf("task ls --json: %v\n%s", err, raw)
	}
	out := map[string]lifeCard{}
	for _, c := range l.Tasks {
		out[c.ID] = c
	}
	return out, raw
}

// waitLife polls the cards until ok, within limit.
func waitLife(t *testing.T, base, session string, limit time.Duration, why string, ok func(map[string]lifeCard) bool) (map[string]lifeCard, string) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for {
		cards, raw := lifeCards(t, base, session)
		if ok(cards) {
			return cards, raw
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: %s\n%s", why, raw)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// inPane runs a tuios command line from inside pane w, as its agent would,
// and returns what it printed with its EXIT= line.
func inPane(t *testing.T, base, session, w, args string) string {
	t.Helper()
	out := filepath.Join(base, "pane-"+strconv.FormatInt(time.Now().UnixNano(), 36)+".out")
	script := tuiosBin + " " + args + " > " + out + ".tmp 2>&1; echo EXIT=$? >> " + out + ".tmp; mv " + out + ".tmp " + out + "\n"
	mustCLI(t, base, "send-text", "-s", session, "-w", w, script)
	return waitForFileText(t, out, func(s string) bool { return strings.Contains(s, "EXIT=") }, "the pane never ran: "+args)
}

// lifeRepo makes a git repository with one commit at dir.
func lifeRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.email=e2e@example.com", "-c", "user.name=e2e", "commit", "-q", "--allow-empty", "-m", "start"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// TestCardLifecycleVerifiesTheAgentsWork: an agent (a pane whose agent state
// the test sets, in a git repository) is done with its card from inside its
// pane. With no verify command the card goes to review. With the board's
// verify command, set by the person, the daemon runs it in the pane's
// worktree: a fail sends the card back to todo with one attempt more and the
// last line of the output as the note, and a pass moves it to review. Then
// the failure modes: a pane cannot set the command or move its card past
// verify; a command that hangs is ended at its timeout, with its children; a
// command that floods keeps only the end of its output; a card moved by hand
// mid-verify keeps its stage and its run is ended; a worktree that is gone
// fails without running the command elsewhere; a restart mid-verify runs it
// again.
//
// How it could pass wrongly: a verify that never ran would leave the card in
// verify, so every step waits for the state the command's own exit gives
// (exit 3 and its last line, a pass); the hang's child process is looked up
// by its pid after the timeout, so a timeout that only stopped waiting fails;
// the hand-moved run's command would leave a marker file if it had lived.
//
// Negative controls (NEGATIVE_CONTROLS.md): with a move out of verify not
// ending the run, the hand-moved card's command makes its marker; with the
// pane refusal of set-task-dispatch verify removed, the pane sets the command;
// with OwnGroup not called, the hang's sleep outlives the timeout; with
// resumeVerifies not called, the card stays in verify after the restart; with
// the tail not cut, the flood keeps 16 KiB.
func TestCardLifecycleVerifiesTheAgentsWork(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	t.Cleanup(func() { dumpDaemonLogOnFail(t, base) })
	const sess = "e2e-life"
	mustCLI(t, base, "new", sess, "--detach")
	waitForSessionInfo(t, base, sess)
	var report strings.Builder
	note := func(format string, args ...any) { fmt.Fprintf(&report, format+"\n", args...) }
	defer func() { saveText(t, "task-lifecycle", report.String()) }()

	repo := filepath.Join(base, "demo")
	lifeRepo(t, repo)
	a := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", sess, "agent-a", "--no-focus", "--print-id", "--cwd", repo))
	mustCLI(t, base, "set-agent-state", "-s", sess, "-w", a, "working", "--harness", "claude-code", "-m", "on it")
	add := func(title string) { mustCLI(t, base, "task", "add", "-s", sess, "-p", "do "+title, title) }
	claim := func(id string) { mustCLI(t, base, "task", "claim", id, "-s", sess, "-w", a) }
	done := func(id string) {
		t.Helper()
		if out := inPane(t, base, sess, a, "task done "+id); !strings.Contains(out, "EXIT=0") {
			t.Fatalf("ASSERTION: tuios task done %s in the agent's pane failed:\n%s", id, out)
		}
	}
	verify := func(args ...string) {
		note("%s", mustCLI(t, base, append([]string{"task", "dispatch", "verify", "-s", sess}, args...)...))
	}

	// No verify command: the agent's done goes to review, and the card is let go.
	add("no check") // t1
	claim("t1")
	done("t1")
	cards, raw := waitLife(t, base, sess, uiTimeout, "t1 did not go to review when its agent was done",
		func(c map[string]lifeCard) bool { return c["t1"].Stage == "review" })
	note("no verify command, agent done:\n%s", raw)
	if cards["t1"].Holder != nil || cards["t1"].Work == nil || filepath.Base(cards["t1"].Work.Dir) != "demo" {
		t.Fatalf("ASSERTION: t1 in review is still held, or has no work in the demo repository: %+v", cards["t1"])
	}

	// The verify command is the person's: a pane is refused.
	out := inPane(t, base, sess, a, "task dispatch verify true")
	note("a pane sets the verify command:\n%s", out)
	if strings.Contains(out, "EXIT=0") || !strings.Contains(out, "refused from every pane") {
		t.Fatalf("ASSERTION: a pane set the board's verify command:\n%s", out)
	}
	verify(`test -f ok || { echo "first line"; echo "not ok yet"; exit 3; }`)

	// A fail: back to todo, one attempt, the last line as the note.
	add("the fix") // t2
	claim("t2")
	done("t2")
	cards, raw = waitLife(t, base, sess, uiTimeout, "t2 did not go back to todo when its verify failed",
		func(c map[string]lifeCard) bool {
			return c["t2"].Stage == "todo" && c["t2"].Verify != nil && c["t2"].Verify.State == "failed"
		})
	note("verify failed:\n%s", raw)
	if c := cards["t2"]; c.Attempts != 1 || c.Note != "exit 3: not ok yet" || c.Holder != nil || c.Verify.Exit == nil || *c.Verify.Exit != 3 ||
		!strings.Contains(c.Verify.Tail, "first line") {
		t.Fatalf("ASSERTION: t2 after the failed verify is %+v (verify %+v), want attempts 1, note \"exit 3: not ok yet\", no holder, exit 3 and the output", c, c.Verify)
	}
	note("%s", mustCLI(t, base, "task", "get", "t2", "-s", sess))

	// The agent cannot move its card past verify by hand.
	claim("t2")
	out = inPane(t, base, sess, a, "task stage t2 review")
	note("the agent moves its card to review:\n%s", out)
	if strings.Contains(out, "EXIT=0") {
		t.Fatalf("ASSERTION: the agent moved t2 to review past the verify:\n%s", out)
	}

	// A pass: review.
	if err := os.WriteFile(filepath.Join(repo, "ok"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	done("t2")
	cards, raw = waitLife(t, base, sess, uiTimeout, "t2 did not go to review when its verify passed",
		func(c map[string]lifeCard) bool { return c["t2"].Stage == "review" })
	note("verify passed:\n%s", raw)
	if c := cards["t2"]; c.Verify == nil || c.Verify.State != "passed" || c.Attempts != 1 {
		t.Fatalf("ASSERTION: t2 in review has verify %+v and attempts %d, want passed and 1", c.Verify, c.Attempts)
	}

	// A hang: ended at its timeout, with the child it started.
	pidFile := filepath.Join(base, "hang.pid")
	verify("sleep 120 & echo $! > "+pidFile+"; wait", "--verify-timeout", "1")
	add("hangs") // t3
	claim("t3")
	start := time.Now()
	done("t3")
	cards, raw = waitLife(t, base, sess, 15*time.Second, "t3 did not fail at its 1 second timeout",
		func(c map[string]lifeCard) bool { return c["t3"].Stage == "todo" })
	note("verify hung (%s):\n%s", time.Since(start).Round(100*time.Millisecond), raw)
	if !strings.Contains(cards["t3"].Note, "timed out after 1s") {
		t.Fatalf("ASSERTION: t3's note is %q, want the timeout", cards["t3"].Note)
	}
	pidText, err := os.ReadFile(pidFile) //nolint:gosec // this test's own file
	if err != nil {
		t.Fatalf("the hang never wrote its child's pid: %v", err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(pidText)))
	gone := false
	for range 30 {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			gone = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	note("the hang's child %d gone after the timeout: %v", pid, gone)
	if !gone {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("ASSERTION: the hang's child %d still runs after the timeout ended its verify", pid)
	}

	// A flood: only the end of the output is kept.
	verify("yes flood-line | head -c 40000000; echo; echo the-last-line; exit 1")
	add("floods") // t4
	claim("t4")
	done("t4")
	cards, raw = waitLife(t, base, sess, 60*time.Second, "t4 did not fail after its flood",
		func(c map[string]lifeCard) bool { return c["t4"].Stage == "todo" })
	note("verify flooded: note %q, tail %d bytes", cards["t4"].Note, len(cards["t4"].Verify.Tail))
	if cards["t4"].Note != "exit 1: the-last-line" || len(cards["t4"].Verify.Tail) > 2048 || len(raw) > 64<<10 {
		t.Fatalf("ASSERTION: t4 after the flood has note %q and a %d byte tail (listing %d bytes), want the last line and at most 2048",
			cards["t4"].Note, len(cards["t4"].Verify.Tail), len(raw))
	}

	// A card moved by hand mid-verify keeps its stage, and the run ends.
	marker := filepath.Join(base, "lived")
	verify("sleep 3; touch " + marker)
	add("moved by hand") // t5
	claim("t5")
	done("t5")
	waitLife(t, base, sess, uiTimeout, "t5 did not go to verify",
		func(c map[string]lifeCard) bool { return c["t5"].Stage == "verify" })
	mustCLI(t, base, "task", "stage", "t5", "doing", "-s", sess)
	time.Sleep(5 * time.Second)
	cards, raw = lifeCards(t, base, sess)
	_, lived := os.Stat(marker)
	note("t5 moved to doing mid-verify, 5s later (marker made: %v):\n%s", lived == nil, raw)
	if cards["t5"].Stage != "doing" {
		t.Fatalf("ASSERTION: t5 moved by hand to doing mid-verify is now %s", cards["t5"].Stage)
	}
	if lived == nil {
		t.Fatalf("ASSERTION: the verify of t5 kept running after the card was moved by hand")
	}

	// A worktree that is gone fails, and the command runs nowhere else.
	gone2 := filepath.Join(base, "gone")
	lifeRepo(t, gone2)
	b := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", sess, "agent-b", "--no-focus", "--print-id", "--cwd", gone2))
	mustCLI(t, base, "set-agent-state", "-s", sess, "-w", b, "working", "--harness", "claude-code", "-m", "on it")
	verify("pwd > " + filepath.Join(base, "ran-in"))
	add("its folder goes") // t6
	mustCLI(t, base, "task", "claim", "t6", "-s", sess, "-w", b)
	if out := inPane(t, base, sess, b, "task done t6"); !strings.Contains(out, "EXIT=0") {
		t.Fatalf("tuios task done t6: %s", out)
	}
	waitLife(t, base, sess, uiTimeout, "t6 did not pass its first verify",
		func(c map[string]lifeCard) bool { return c["t6"].Stage == "review" })
	mustCLI(t, base, "close-window", "-s", sess, b)
	if err := os.RemoveAll(gone2); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(base, "ran-in"))
	mustCLI(t, base, "task", "stage", "t6", "verify", "-s", sess)
	cards, raw = waitLife(t, base, sess, uiTimeout, "t6 did not fail when its worktree was gone",
		func(c map[string]lifeCard) bool { return c["t6"].Stage == "todo" })
	note("t6 verified again with its worktree gone:\n%s", raw)
	if cards["t6"].Note != "the worktree is gone" || cards["t6"].Attempts != 1 {
		t.Fatalf("ASSERTION: t6 has note %q and attempts %d, want the worktree is gone and 1", cards["t6"].Note, cards["t6"].Attempts)
	}
	if _, err := os.Stat(filepath.Join(base, "ran-in")); err == nil {
		t.Fatalf("ASSERTION: the verify of t6 ran somewhere else when its worktree was gone")
	}

	// A restart mid-verify runs it again.
	verify("sleep 2")
	add("across a restart") // t7
	claim("t7")
	done("t7")
	waitLife(t, base, sess, uiTimeout, "t7 did not go to verify",
		func(c map[string]lifeCard) bool { return c["t7"].Stage == "verify" })
	mustCLI(t, base, "restart-server", "--yes")
	mustCLI(t, base, "new", "e2e-life-other", "--detach")
	waitForSessionInfo(t, base, sess)
	cards, raw = waitLife(t, base, sess, 20*time.Second, "t7 did not reach review after a restart mid-verify",
		func(c map[string]lifeCard) bool { return c["t7"].Stage == "review" })
	note("after a restart mid-verify:\n%s", raw)
	if cards["t7"].Verify == nil || cards["t7"].Verify.State != "passed" {
		t.Fatalf("ASSERTION: t7 in review has verify %+v, want passed", cards["t7"].Verify)
	}
	note("%s", mustCLI(t, base, "task", "get", "t2", "-s", sess))
}

// TestCardLifecycleCountsOnAPullBoard: on a pull board, a dispatched card
// whose verify fails counts one failure, a pass resets the count, and an
// agent that errors puts its card back to todo with its last line as the
// note and one attempt more. The agent is free for its next card as soon as
// it is done, while the verify runs.
//
// How it could pass wrongly: the count could stay 0 because nothing was
// dispatched, so the cards are checked as dispatched and the board's failures
// are read after each step.
//
// Negative controls: with the verify outcome left out of
// noteDispatchedOutcome, the count after the failed verify is 0; with the
// errored agent's line not kept, the note is the old verify's.
func TestCardLifecycleCountsOnAPullBoard(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	t.Cleanup(func() { dumpDaemonLogOnFail(t, base) })
	const sess = "e2e-life-pull"
	mustCLI(t, base, "new", sess, "--detach")
	waitForSessionInfo(t, base, sess)
	var report strings.Builder
	note := func(format string, args ...any) { fmt.Fprintf(&report, format+"\n", args...) }
	defer func() { saveText(t, "task-lifecycle-pull", report.String()) }()

	repo := filepath.Join(base, "demo")
	lifeRepo(t, repo)
	a := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", sess, "agent-a", "--no-focus", "--print-id", "--cwd", repo))
	mustCLI(t, base, "set-agent-state", "-s", sess, "-w", a, "working", "--harness", "claude-code", "-m", "on it")
	for _, title := range []string{"one", "two", "three"} {
		mustCLI(t, base, "task", "add", "-s", sess, "-p", "do "+title, title)
	}
	note("%s", mustCLI(t, base, "task", "dispatch", "mode", "pull", "-s", sess))
	note("%s", mustCLI(t, base, "task", "dispatch", "verify", "-s", sess, `test -f ok || { echo no ok file; exit 1; }`))
	failures := func() int {
		raw := mustCLI(t, base, "task", "dispatch", "-s", sess, "--json")
		var st struct {
			Board *struct {
				Failures int `json:"failures"`
			} `json:"board"`
		}
		if err := json.Unmarshal([]byte(raw), &st); err != nil || st.Board == nil {
			t.Fatalf("task dispatch --json: %v\n%s", err, raw)
		}
		return st.Board.Failures
	}

	// The agent is done with t1 (claimed by hand); pull gives it t2 at once.
	mustCLI(t, base, "task", "claim", "t1", "-s", sess, "-w", a)
	if out := inPane(t, base, sess, a, "task done t1"); !strings.Contains(out, "EXIT=0") {
		t.Fatalf("tuios task done t1: %s", out)
	}
	cards, raw := waitLife(t, base, sess, uiTimeout, "pull did not give the agent t2 after it was done with t1",
		func(c map[string]lifeCard) bool { return c["t2"].Holder != nil && c["t2"].Holder.Window == a })
	note("after the agent was done with t1:\n%s", raw)

	// t2 is dispatched; its verify fails: one failure.
	if out := inPane(t, base, sess, a, "task done t2"); !strings.Contains(out, "EXIT=0") {
		t.Fatalf("tuios task done t2: %s", out)
	}
	cards, raw = waitLife(t, base, sess, uiTimeout, "t2 did not go back to todo when its verify failed",
		func(c map[string]lifeCard) bool { return c["t2"].Stage == "todo" && c["t2"].Attempts == 1 })
	n := failures()
	note("after t2's verify failed (failures %d):\n%s", n, raw)
	if n != 1 {
		t.Fatalf("ASSERTION: the board counts %d failures after a dispatched card's verify failed, want 1", n)
	}

	// The agent now holds the next card; it errors: the card stays with it,
	// blocked with its last line, and goes back to todo once let go.
	cards, _ = lifeCards(t, base, sess)
	held := ""
	for id, c := range cards {
		if c.Holder != nil && c.Holder.Window == a {
			held = id
		}
	}
	if held == "" {
		t.Fatalf("ASSERTION: the agent holds no card after it was done with t2:\n%s", raw)
	}
	mustCLI(t, base, "set-agent-state", "-s", sess, "-w", a, "errored", "--harness", "claude-code", "-m", "the API said no")
	cards, raw = waitLife(t, base, sess, uiTimeout, held+" was not blocked on its errored agent",
		func(c map[string]lifeCard) bool { return c[held].Attempts >= 1 && c[held].Note == "the API said no" })
	note("after the agent errored holding %s (failures %d):\n%s", held, failures(), raw)
	if c := cards[held]; c.Holder == nil || c.Holder.Window != a || c.Stage != "doing" {
		t.Fatalf("ASSERTION: %s after its agent errored is %s held by %+v, want doing and still held by the agent", held, c.Stage, c.Holder)
	}
	if out := inPane(t, base, sess, a, "task release "+held); !strings.Contains(out, "EXIT=0") {
		t.Fatalf("tuios task release %s: %s", held, out)
	}
	cards, raw = waitLife(t, base, sess, uiTimeout, held+" did not go back to todo when the errored agent let it go",
		func(c map[string]lifeCard) bool { return c[held].Holder == nil && c[held].Stage == "todo" })
	note("after the errored agent let %s go (failures %d):\n%s", held, failures(), raw)

	// A pass resets the count.
	if err := os.WriteFile(filepath.Join(repo, "ok"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	mustCLI(t, base, "set-agent-state", "-s", sess, "-w", a, "working", "--harness", "claude-code", "-m", "again")
	mustCLI(t, base, "task", "claim", "t3", "-s", sess, "-w", a)
	// The claim by hand is not dispatched, so pull gives t3 again: let the
	// agent take one by dispatch first.
	if out := inPane(t, base, sess, a, "task done t3"); !strings.Contains(out, "EXIT=0") {
		t.Fatalf("tuios task done t3: %s", out)
	}
	cards, raw = waitLife(t, base, sess, uiTimeout, "pull did not give the agent a card after t3",
		func(c map[string]lifeCard) bool {
			for _, x := range c {
				if x.Holder != nil && x.Holder.Window == a {
					return true
				}
			}
			return false
		})
	next := ""
	for id, c := range cards {
		if c.Holder != nil && c.Holder.Window == a {
			next = id
		}
	}
	if out := inPane(t, base, sess, a, "task done "+next); !strings.Contains(out, "EXIT=0") {
		t.Fatalf("tuios task done %s: %s", next, out)
	}
	_, raw = waitLife(t, base, sess, uiTimeout, next+" did not reach review when its verify passed",
		func(c map[string]lifeCard) bool { return c[next].Stage == "review" })
	n = failures()
	note("after %s's verify passed (failures %d):\n%s", next, n, raw)
	if n != 0 {
		t.Fatalf("ASSERTION: the board counts %d failures after a dispatched card's verify passed, want 0", n)
	}
}

// TestCardLifecycleVerifiesOnTheCardsMachine: two machines, home (owns the
// card) and build. A pane on build claims home's card and is done with it.
// Home runs the board's verify command on build, in that pane's checkout,
// through run-task-verify over the link: a fail sends the card back to todo,
// a pass moves it to review. When build is down, verifying the card again
// leaves it in verify with the reason, and counts no attempt.
//
// How it could pass wrongly: the command could run on home instead, so it
// tests for a file that only build's checkout has.
//
// Negative control: with the far card verified here instead, the note says
// the worktree is gone.
func TestCardLifecycleVerifiesOnTheCardsMachine(t *testing.T) {
	pp := startPool(t, 0, true)
	t.Cleanup(func() { dumpDaemonLogOnFail(t, pp.base); dumpDaemonLogOnFail(t, pp.remote) })
	var report strings.Builder
	note := func(format string, args ...any) { fmt.Fprintf(&report, format+"\n", args...) }
	defer func() { saveText(t, "task-lifecycle-far", report.String()) }()

	const proj = "https://example.com/acme/api.git"
	mustCLI(t, pp.base, "task", "add", "-s", "home", "--project", proj, "-p", "Fix it on build.", "far work") // t1
	note("%s", mustCLI(t, pp.base, "task", "dispatch", "verify", "-s", "home", "--project", proj,
		`test -f far-ok || { echo far says no; exit 4; }`))
	w := strings.TrimSpace(mustCLI(t, pp.remote, "new-window", "-s", "far", "far-agent", "--no-focus", "--print-id", "--cwd", pp.farCheckout))
	mustCLI(t, pp.remote, "set-agent-state", "-s", "far", "-w", w, "working", "--harness", "claude-code", "-m", "on it")
	run := func(args string) {
		t.Helper()
		if out := inPane(t, pp.remote, "far", w, args); !strings.Contains(out, "EXIT=0") {
			t.Fatalf("ASSERTION: %s in build's pane failed:\n%s", args, out)
		}
	}

	run("task claim t1 -s home:home")
	run("task done t1 -s home:home")
	cards, raw := waitLife(t, pp.base, "home", 30*time.Second, "t1 did not go back to todo when its verify failed on build",
		func(c map[string]lifeCard) bool { return c["t1"].Stage == "todo" })
	note("verify failed on build:\n%s", raw)
	if c := cards["t1"]; c.Note != "exit 4: far says no" || c.Attempts != 1 || c.Work == nil || c.Work.Host == "" {
		t.Fatalf("ASSERTION: t1 after the failed far verify is %+v (work %+v), want note \"exit 4: far says no\", attempts 1, work on the far machine", c, c.Work)
	}

	if err := os.WriteFile(filepath.Join(pp.farCheckout, "far-ok"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	run("task claim t1 -s home:home")
	run("task done t1 -s home:home")
	cards, raw = waitLife(t, pp.base, "home", 30*time.Second, "t1 did not reach review when its verify passed on build",
		func(c map[string]lifeCard) bool { return c["t1"].Stage == "review" })
	note("verify passed on build:\n%s", raw)

	// build goes down: verify again, and the card waits in verify.
	killDaemonNow(t, pp.remote)
	mustCLI(t, pp.base, "task", "stage", "t1", "verify", "-s", "home")
	cards, raw = waitLife(t, pp.base, "home", 45*time.Second, "t1 did not record that build was down",
		func(c map[string]lifeCard) bool { return c["t1"].Verify != nil && c["t1"].Verify.State == "failed" })
	note("verify with build down:\n%s", raw)
	if c := cards["t1"]; c.Stage != "verify" || c.Attempts != 1 || !strings.Contains(c.Verify.Note, "build") {
		t.Fatalf("ASSERTION: t1 with build down is in %s with attempts %d and verify note %q, want verify, 1, and build named", c.Stage, c.Attempts, c.Verify.Note)
	}
}

// TestAMoveToVerifyLetsTheHolderGo: on a board with a verify command, the
// person moves a card a pane holds to verify. The card is let go at once,
// since it now waits on the command and not on a pane, and the verify runs
// and moves it to review with no holder.
//
// How it could pass wrongly: a verify that never ran would leave the card in
// verify, so the test waits for review and reads the record as passed.
//
// Negative controls (NEGATIVE_CONTROLS.md, TestAMoveToVerifyLetsTheHolderGo).
func TestAMoveToVerifyLetsTheHolderGo(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	t.Cleanup(func() { dumpDaemonLogOnFail(t, base) })
	const sess = "e2e-stage-verify"
	mustCLI(t, base, "new", sess, "--detach")
	waitForSessionInfo(t, base, sess)
	var report strings.Builder
	note := func(format string, args ...any) { fmt.Fprintf(&report, format+"\n", args...) }
	defer func() { saveText(t, "task-stage-verify", report.String()) }()

	repo := filepath.Join(base, "demo")
	lifeRepo(t, repo)
	a := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", sess, "agent-a", "--no-focus", "--print-id", "--cwd", repo))
	mustCLI(t, base, "set-agent-state", "-s", sess, "-w", a, "working", "--harness", "claude-code", "-m", "on it")
	note("%s", mustCLI(t, base, "task", "dispatch", "verify", "-s", sess, "sleep 1"))
	mustCLI(t, base, "task", "add", "-s", sess, "-p", "do it", "the work") // t1
	mustCLI(t, base, "task", "claim", "t1", "-s", sess, "-w", a)
	note("%s", mustCLI(t, base, "task", "stage", "t1", "verify", "-s", sess))
	cards, raw := lifeCards(t, base, sess)
	note("right after the move to verify:\n%s", raw)
	if c := cards["t1"]; c.Stage != "verify" || c.Holder != nil {
		t.Errorf("ASSERTION: t1 after a move to verify is %s held by %+v, want verify and no holder", c.Stage, c.Holder)
	}
	cards, raw = waitLife(t, base, sess, 20*time.Second, "t1 did not reach review after its verify",
		func(c map[string]lifeCard) bool { return c["t1"].Stage == "review" })
	note("after the verify:\n%s", raw)
	if c := cards["t1"]; c.Holder != nil || c.Verify == nil || c.Verify.State != "passed" {
		t.Errorf("ASSERTION: t1 in review is held by %+v with verify %+v, want no holder and passed", c.Holder, c.Verify)
	}
}
