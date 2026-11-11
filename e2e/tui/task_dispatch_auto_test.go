package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// autoCard is one card of tuios task ls --json, with the fields the auto
// tests read.
type autoCard struct {
	ID         string `json:"id"`
	Stage      string `json:"stage"`
	Dispatched bool   `json:"dispatched"`
	Holder     *struct {
		Window string `json:"window"`
		Host   string `json:"host"`
	} `json:"holder"`
}

// autoCards is the session's cards by id.
func autoCards(t *testing.T, base, session string) (map[string]autoCard, string) {
	t.Helper()
	raw := mustCLI(t, base, "task", "ls", "-s", session, "--json")
	var l struct {
		Tasks []autoCard `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(raw), &l); err != nil {
		t.Fatalf("task ls --json: %v\n%s", err, raw)
	}
	out := map[string]autoCard{}
	for _, c := range l.Tasks {
		out[c.ID] = c
	}
	return out, raw
}

// waitAutoCards polls the cards until ok, and returns the last listing.
func waitAutoCards(t *testing.T, base, session, why string, ok func(map[string]autoCard) bool) (map[string]autoCard, string) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		cards, raw := autoCards(t, base, session)
		if ok(cards) {
			return cards, raw
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: %s\n%s\n%s", why, raw, mustCLI(t, base, "task", "dispatch"))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// claudeAgents counts the Claude Code panes in list-agents --json.
func claudeAgents(t *testing.T, raw string) int {
	t.Helper()
	var l struct {
		Agents []struct {
			Harness string `json:"harness_id"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(raw), &l); err != nil {
		t.Fatalf("list-agents --json: %v\n%s", err, raw)
	}
	n := 0
	for _, a := range l.Agents {
		if a.Harness == "claude-code" {
			n++
		}
	}
	return n
}

// held reports whether card id is held.
func held(cards map[string]autoCard, id string) bool { return cards[id].Holder != nil }

// waitDispatchText polls tuios task dispatch until its text has want.
func waitDispatchText(t *testing.T, base, want, why string) string {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		out := mustCLI(t, base, "task", "dispatch")
		if strings.Contains(out, want) {
			return out
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: %s: tuios task dispatch never said %q:\n%s", why, want, out)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// gitIn runs git in dir and returns its output.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// TestAutoDispatchStartsAnAgentPerCard: two machines, home (loaded) and build
// (idle), linked both ways. A project board on home in auto mode, with a cap
// of 2 agents in total and 1 on one machine. Two clients turn auto on at the
// same moment, and a third resumes. With the preset on this machine, the
// default, the first card starts on home and the second waits, since home is
// at its cap. With the preset turned to any machine, the second starts on
// build: exactly two agents. Each runs in a new worktree on a branch named
// from its card's title, holds its card (marked dispatched), and has the card
// as mail from tuios with the command that finishes it there. Nothing typed
// the card into a pane: the agent's screen shows only the fixed mail wake
// line. The ask-first card is never started, and the fourth card waits for a
// free machine until the second finishes. Pause holds the fifth card; resume
// starts it. When build goes down, its card stays held and no second agent
// starts for it.
//
// How it could pass wrongly: the two clients' mode calls could run one after
// the other and still start too many agents later, so the test counts every
// agent pane on both machines after the starts settle; a card could look
// held because a person claimed it, so the test checks dispatched; the mail
// could come from a sender other than tuios; a pause could seem to hold
// because nothing ran, so the test waits for the status line that names the
// pause.
//
// Negative controls (NEGATIVE_CONTROLS.md): with in-flight starts left out
// of the count, three agents start; with machine_cap not excluding build,
// both agents land on build; with task_mail ignored, the agent has no mail.
func TestAutoDispatchStartsAnAgentPerCard(t *testing.T) {
	pp := startPool(t, 0, true)
	t.Cleanup(func() { dumpDaemonLogOnFail(t, pp.base); dumpDaemonLogOnFail(t, pp.remote) })
	var report strings.Builder
	note := func(format string, args ...any) { fmt.Fprintf(&report, format+"\n", args...) }
	defer func() { saveText(t, "task-auto-dispatch", report.String()) }()

	const proj = "https://example.com/acme/api.git"
	add := func(args ...string) {
		mustCLI(t, pp.base, append([]string{"task", "add", "-s", "home", "--project", proj}, args...)...)
	}
	add("-p", "Time out a call after 30 seconds.", "Add a timeout") // t1
	add("-p", "Make the client retry on a 503.", "Fix the retry")   // t2
	add("--ask-first", "-p", "Needs a person.", "Ask me first")     // t3
	add("-p", "Say how retries work.", "Write the docs")            // t4
	note("%s", mustCLI(t, pp.base, "task", "dispatch", "cap", "2", "--machine-cap", "1"))

	// Two clients turn auto on at once, and a third resumes.
	var wg sync.WaitGroup
	outs := make([]string, 3)
	for i, args := range [][]string{
		{"task", "dispatch", "mode", "auto", "--agent", "claude", "--project", proj, "-s", "home"},
		{"task", "dispatch", "mode", "auto", "--agent", "claude", "--project", proj, "-s", "home"},
		{"task", "dispatch", "resume"},
	} {
		wg.Go(func() {
			out, err := tuiosCLI(t, pp.base, args...)
			outs[i] = fmt.Sprintf("$ tuios %s\n%s (err %v)", strings.Join(args, " "), out, err)
		})
	}
	wg.Wait()
	note("%s", strings.Join(outs, "\n"))

	// The preset's machine is this one unless the person picks another:
	// t1 starts on home, and t2 waits, as home is at its cap and build is
	// not picked.
	cards, raw := waitAutoCards(t, pp.base, "home", "auto did not start t1",
		func(c map[string]autoCard) bool { return held(c, "t1") })
	note("cards with the preset on this machine:\n%s\n%s", raw, waitDispatchText(t, pp.base, "2 waiting (each machine it may use is full)", "t2 does not wait while the preset is this machine"))
	if cards["t1"].Holder.Host != "" || held(cards, "t2") {
		t.Fatalf("ASSERTION: with the preset on this machine, want t1 on home and t2 waiting:\n%s", raw)
	}
	note("%s", mustCLI(t, pp.base, "task", "dispatch", "preset", "--agent", "claude", "--machine", "any", "--project", proj, "-s", "home"))
	cards, raw = waitAutoCards(t, pp.base, "home", "auto did not start t2 on build after the preset turned to any",
		func(c map[string]autoCard) bool { return held(c, "t2") && held(c, "t1") })
	note("cards after auto turned on:\n%s", raw)
	for _, id := range []string{"t2", "t1"} {
		if !cards[id].Dispatched {
			t.Errorf("ASSERTION: %s is held but not marked dispatched", id)
		}
	}
	if cards["t2"].Holder.Host == "" {
		t.Errorf("ASSERTION: t2 is not held by a pane on build: %+v", cards["t2"].Holder)
	}
	if cards["t1"].Holder.Host != "" {
		t.Errorf("ASSERTION: t1 is not held by a pane on home: %+v", cards["t1"].Holder)
	}
	// The starts settle: no third agent, t3 and t4 untouched.
	status := waitDispatchText(t, pp.base, "1 waiting", "t4 does not wait for a free machine")
	note("status:\n%s", status)
	time.Sleep(3 * time.Second)
	cards, raw = autoCards(t, pp.base, "home")
	if held(cards, "t3") || held(cards, "t4") {
		t.Fatalf("ASSERTION: auto started t3 (ask first) or t4 (over the cap):\n%s", raw)
	}
	homeAgents := mustCLI(t, pp.base, "list-agents", "--all-sessions", "--json")
	buildAgents := mustCLI(t, pp.remote, "list-agents", "--all-sessions", "--json")
	note("home list-agents:\n%s\nbuild list-agents:\n%s", homeAgents, buildAgents)
	if n := claudeAgents(t, homeAgents) + claudeAgents(t, buildAgents); n != 2 {
		t.Fatalf("ASSERTION: %d claude agents run on the two machines, want 2 (the cap)", n)
	}

	// The worktrees, on branches from the titles.
	if got := gitIn(t, pp.here, "branch", "--list", "task/*"); !strings.Contains(got, "task/add-a-timeout") {
		t.Errorf("ASSERTION: home has no branch task/add-a-timeout:\n%s", got)
	}
	if got := gitIn(t, pp.farCheckout, "branch", "--list", "task/*"); !strings.Contains(got, "task/fix-the-retry") {
		t.Errorf("ASSERTION: build has no branch task/fix-the-retry:\n%s", got)
	}

	// The card reached each agent as mail from tuios.
	homeSess := "api-task-add-a-timeout"
	mail := mustCLI(t, pp.base, "read-agent-messages", "-s", homeSess, "-w", cards["t1"].Holder.Window, "--peek", "--json")
	note("home agent's mail:\n%s", mail)
	if !strings.Contains(mail, "Time out a call after 30 seconds.") || !strings.Contains(mail, "tuios task done t1 -s home") || !strings.Contains(mail, `"from":"tuios"`) && !strings.Contains(mail, `"from": "tuios"`) {
		t.Fatalf("ASSERTION: home's agent has no mail from tuios with t1 and its done command:\n%s", mail)
	}
	farMail := mustCLI(t, pp.remote, "read-agent-messages", "-s", "api-task-fix-the-retry", "-w", cards["t2"].Holder.Window, "--peek", "--json")
	note("build agent's mail:\n%s", farMail)
	if !strings.Contains(farMail, "Make the client retry on a 503.") || !strings.Contains(farMail, "tuios task done t2 -s home:home") {
		t.Fatalf("ASSERTION: build's agent has no mail with t2 and its done command over the link:\n%s", farMail)
	}
	// Nothing typed the card: the agent read only the fixed wake line.
	screen := mustCLI(t, pp.base, "capture-pane", "-s", homeSess, "-w", cards["t1"].Holder.Window)
	note("home agent's screen:\n%s", screen)
	if strings.Contains(screen, "Time out a call") {
		t.Fatalf("ASSERTION: the card's prompt was typed into the agent's pane:\n%s", screen)
	}

	// t1 finishes: its slot goes to t4, on home (build is still at its cap).
	mustCLI(t, pp.base, "task", "done", "t1", "-s", "home")
	cards, raw = waitAutoCards(t, pp.base, "home", "t4 did not start after t1 finished",
		func(c map[string]autoCard) bool { return held(c, "t4") })
	note("after t1 finished:\n%s", raw)
	if cards["t4"].Holder.Host != "" {
		t.Errorf("ASSERTION: t4 started on build, which is at its cap: %+v", cards["t4"].Holder)
	}

	// Pause holds a new card; resume starts it.
	note("%s", mustCLI(t, pp.base, "task", "dispatch", "pause"))
	add("-p", "Added while paused.", "After the pause") // t5
	mustCLI(t, pp.base, "task", "done", "t4", "-s", "home")
	note("status while paused:\n%s", waitDispatchText(t, pp.base, "dispatch is paused", "the pause did not hold t5"))
	if cards, raw := autoCards(t, pp.base, "home"); held(cards, "t5") {
		t.Fatalf("ASSERTION: t5 started while dispatch was paused:\n%s", raw)
	}
	note("%s", mustCLI(t, pp.base, "task", "dispatch", "resume"))
	_, raw = waitAutoCards(t, pp.base, "home", "t5 did not start after resume",
		func(c map[string]autoCard) bool { return held(c, "t5") })
	note("after resume:\n%s", raw)

	// build goes down: its card stays held, and no second agent starts.
	before := claudeAgents(t, mustCLI(t, pp.base, "list-agents", "--all-sessions", "--json"))
	killDaemonNow(t, pp.remote)
	time.Sleep(5 * time.Second)
	cards, raw = autoCards(t, pp.base, "home")
	note("after build went down:\n%s\n%s", raw, mustCLI(t, pp.base, "task", "dispatch"))
	if !held(cards, "t2") || cards["t2"].Holder.Host == "" {
		t.Fatalf("ASSERTION: t2 is no longer held by build's pane after build went down:\n%s", raw)
	}
	if n := claudeAgents(t, mustCLI(t, pp.base, "list-agents", "--all-sessions", "--json")); n != before {
		t.Fatalf("ASSERTION: home runs %d agents after build went down, want %d as before: one started for t2", n, before)
	}
	if ls := mustCLI(t, pp.base, "ls"); strings.Contains(ls, "fix-the-retry") {
		t.Fatalf("ASSERTION: home made a worktree for t2 after build went down:\n%s", ls)
	}
}

// TestAutoDispatchSafety: one machine, a session board in auto mode. The
// person already has a branch named from the first card's title, so auto
// takes the next name and leaves the person's branch alone. A preset whose
// agent no machine has stops the board at once. An agent that exits as it
// starts fails three cards in a row, and the board stops after the third;
// the cards it let go stay in doing and are not started again.
//
// How it could pass wrongly: a branch that exists could be checked out
// with no error, so the test reads git's worktree list; the stop could be
// the 3 failures and not the missing agent, so that check comes first, on a
// board with no failures.
//
// Negative controls (NEGATIVE_CONTROLS.md): with new_branch ignored, the
// worktree is on the person's branch; with the not-installed stop removed,
// the board does not stop for the missing agent.
func TestAutoDispatchSafety(t *testing.T) {
	base, origin := fanFixture(t)
	t.Setenv("TUIOS_WORKTREE_DIR", "")
	var report strings.Builder
	note := func(format string, args ...any) { fmt.Fprintf(&report, format+"\n", args...) }
	defer func() { saveText(t, "task-auto-dispatch-safety", report.String()) }()
	t.Cleanup(func() { dumpDaemonLogOnFail(t, base) })
	// codex here is a program that exits at once, on the daemon's PATH.
	quitter := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(quitter, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(quitter, "codex"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", quitter+string(os.PathListSeparator)+os.Getenv("PATH"))
	// An agent that never comes up is given 5 seconds, not 2 minutes.
	t.Setenv("TUIOS_DISPATCH_READY_SECONDS", "5")
	here := filepath.Join(base, "work", "api")
	cloneWithOrigin(t, origin, here, "git@example.com:acme/api")
	gitIn(t, here, "branch", "task/fix-the-retry")
	mustCLI(t, base, "new", "home", "--detach", "--cwd", here)
	waitForSessionInfo(t, base, "home")

	mustCLI(t, base, "task", "add", "-s", "home", "-p", "Retry on a 503.", "Fix the retry") // t1
	note("%s", mustCLI(t, base, "task", "dispatch", "mode", "auto", "--agent", "claude", "-s", "home"))
	waitAutoCards(t, base, "home", "auto did not start t1", func(c map[string]autoCard) bool { return held(c, "t1") })
	wts := gitIn(t, here, "worktree", "list")
	note("git worktree list:\n%s", wts)
	if !strings.Contains(wts, "[task/fix-the-retry-2]") {
		t.Fatalf("ASSERTION: t1's worktree is not on task/fix-the-retry-2:\n%s", wts)
	}
	if strings.Contains(wts, "[task/fix-the-retry]") {
		t.Fatalf("ASSERTION: auto checked out the person's branch task/fix-the-retry:\n%s", wts)
	}

	// A preset whose agent is on no machine stops the board at once.
	note("%s", mustCLI(t, base, "task", "dispatch", "preset", "--agent", "nosuchagent", "-s", "home"))
	mustCLI(t, base, "task", "add", "-s", "home", "-p", "Two.", "Second card") // t2
	status := waitDispatchText(t, base, "nosuchagent is not installed on any machine", "the board did not stop for the missing agent")
	note("status:\n%s", status)
	if strings.Contains(status, "failed in a row") || strings.Contains(status, "3 failures") {
		t.Fatalf("ASSERTION: the missing agent counted as failures:\n%s", status)
	}

	// An agent that exits as it starts: three failures stop the board.
	note("%s", mustCLI(t, base, "task", "dispatch", "preset", "--agent", "codex", "-s", "home"))
	mustCLI(t, base, "task", "done", "t1", "-s", "home")
	note("%s", mustCLI(t, base, "task", "dispatch", "cap", "1"))
	mustCLI(t, base, "task", "add", "-s", "home", "-p", "Three.", "Third card") // t3
	mustCLI(t, base, "task", "add", "-s", "home", "-p", "Four.", "Fourth card") // t4
	mustCLI(t, base, "task", "add", "-s", "home", "-p", "Five.", "Fifth card")  // t5
	note("%s", mustCLI(t, base, "task", "dispatch", "resume"))
	status = waitDispatchText(t, base, "stopped: 3 failures in a row", "three failed starts did not stop the board")
	note("status:\n%s", status)
	time.Sleep(3 * time.Second)
	cards, raw := autoCards(t, base, "home")
	note("cards:\n%s", raw)
	failed := 0
	for _, id := range []string{"t2", "t3", "t4", "t5"} {
		if held(cards, id) {
			t.Errorf("ASSERTION: %s is still held after its agent exited", id)
		}
		if cards[id].Stage == "doing" {
			failed++
		}
	}
	if failed != 3 {
		t.Fatalf("ASSERTION: %d cards were tried, want 3 before the stop:\n%s", failed, raw)
	}
}

// dumpDaemonLogOnFail prints the end of base's daemon log when t failed.
func dumpDaemonLogOnFail(t *testing.T, base string) {
	if !t.Failed() {
		return
	}
	data, err := os.ReadFile(daemonLogOf(t, base))
	if err != nil {
		t.Logf("no daemon log: %v", err)
		return
	}
	out, _ := tuiosCLI(t, base, "list-agents", "--json")
	cards, _ := tuiosCLI(t, base, "task", "ls", "-s", "home")
	ls, _ := tuiosCLI(t, base, "ls")
	for _, name := range []string{"api-task-second-card", "api-task-add-a-timeout"} {
		w, _ := tuiosCLI(t, base, "list-windows", "-s", name, "--json")
		ls += "\n" + name + ":\n" + w
		var l struct {
			Windows []struct {
				ID string `json:"id"`
			} `json:"windows"`
		}
		_ = json.Unmarshal([]byte(w), &l)
		for _, win := range l.Windows {
			c, _ := tuiosCLI(t, base, "capture-pane", "-s", name, "-w", win.ID)
			ls += "\n--- " + win.ID + ":\n" + c
		}
	}
	out += "\n" + ls
	var lines []string
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.Contains(line, "dispatch") || strings.Contains(line, "panic") || strings.Contains(line, "task") {
			lines = append(lines, line)
		}
	}
	t.Logf("agents of %s:\n%s\ncards:\n%s\ndaemon log (%d bytes):\n%s", base, out, cards, len(data), strings.Join(lines, "\n"))
}

// TestAutoDispatchFailedStartsLeaveNothingBehind: on an auto board, an agent
// that errors while it holds its card keeps the card, marked blocked with
// its last line, and no second agent starts for it while the first lives.
// Once that pane closes, the card goes back to todo and auto starts it again.
// Then a start whose agent never comes up: auto lets the card go, and closes
// the pane, the session and the worktree it made for it. The errored agent
// counts one failure, when its card is let go, not two.
//
// How it could pass wrongly: a second agent could start late, so the test
// waits past the time a start takes on this board before it counts agents;
// a leftover pane is looked for by its program, which the fake agent runs.
//
// Negative controls (NEGATIVE_CONTROLS.md, TestAutoDispatchFailedStartsLeaveNothingBehind).
func TestAutoDispatchFailedStartsLeaveNothingBehind(t *testing.T) {
	base, origin := fanFixture(t)
	t.Setenv("TUIOS_WORKTREE_DIR", "")
	var report strings.Builder
	note := func(format string, args ...any) { fmt.Fprintf(&report, format+"\n", args...) }
	defer func() { saveText(t, "task-auto-dispatch-failed-starts", report.String()) }()
	t.Cleanup(func() { dumpDaemonLogOnFail(t, base) })
	// codex here never comes up and never exits.
	stall := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(stall, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stall, "codex"), []byte("#!/bin/sh\nexec sleep 987\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stall+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TUIOS_DISPATCH_READY_SECONDS", "3")
	here := filepath.Join(base, "work", "api")
	cloneWithOrigin(t, origin, here, "git@example.com:acme/api")
	mustCLI(t, base, "new", "home", "--detach", "--cwd", here)
	waitForSessionInfo(t, base, "home")

	type card struct {
		Stage    string `json:"stage"`
		Attempts int    `json:"attempts"`
		Note     string `json:"note"`
		Holder   *struct {
			Window string `json:"window"`
		} `json:"holder"`
		Blocked *struct {
			Reason string `json:"reason"`
		} `json:"blocked"`
	}
	cardOf := func(id string) (card, string) {
		raw := mustCLI(t, base, "task", "get", id, "-s", "home", "--json")
		var c card
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			t.Fatalf("task get %s --json: %v\n%s", id, err, raw)
		}
		return c, raw
	}

	// An agent that errors keeps its card, and no second agent starts.
	mustCLI(t, base, "task", "add", "-s", "home", "-p", "Retry on a 503.", "Fix the retry") // t1
	note("%s", mustCLI(t, base, "task", "dispatch", "mode", "auto", "--agent", "claude", "-s", "home"))
	cards, _ := waitAutoCards(t, base, "home", "auto did not start t1", func(c map[string]autoCard) bool { return held(c, "t1") })
	first := cards["t1"].Holder.Window
	waitUntil(t, "the agent of t1 never came up", 30*time.Second, func() (bool, string) {
		n := claudeAgents(t, mustCLI(t, base, "list-agents", "--json"))
		return n == 1, fmt.Sprintf("%d claude agents", n)
	})
	mustCLI(t, base, "set-agent-state", "-s", "api-task-fix-the-retry", "-w", first, "errored", "--harness", "claude-code", "-m", "the API said no")
	time.Sleep(8 * time.Second) // longer than a start of the fake agent takes
	c, raw := cardOf("t1")
	ls := mustCLI(t, base, "ls")
	note("t1 after its agent errored:\n%s\nsessions:\n%s", raw, ls)
	if strings.Contains(ls, "api-task-fix-the-retry-2") {
		t.Errorf("ASSERTION: a second agent started for t1 beside the first, in api-task-fix-the-retry-2:\n%s", ls)
	}
	if c.Holder == nil || c.Holder.Window != first || c.Blocked == nil || !strings.Contains(c.Blocked.Reason, "the API said no") || c.Attempts != 1 {
		t.Fatalf("ASSERTION: t1 after its agent errored is %s; want held by the first agent %s, blocked with its last line, one attempt", raw, first)
	}
	// The pane closes: the card goes back and auto starts it again.
	mustCLI(t, base, "close-window", "-s", "api-task-fix-the-retry", first)
	cards, raw = waitAutoCards(t, base, "home", "t1 was not started again after its errored agent's pane closed",
		func(c map[string]autoCard) bool { return held(c, "t1") && c["t1"].Holder.Window != first })
	note("t1 after the errored pane closed:\n%s", raw)
	if c, raw := cardOf("t1"); c.Blocked != nil {
		t.Fatalf("ASSERTION: t1 is still blocked after its errored agent left: %s", raw)
	}

	// A start that never comes up: nothing it opened is left.
	note("%s", mustCLI(t, base, "task", "dispatch", "preset", "--agent", "codex", "-s", "home"))
	mustCLI(t, base, "task", "add", "-s", "home", "-p", "Stalls.", "Never comes up") // t2
	waitUntil(t, "t2 was never let go after its agent did not come up", 90*time.Second, func() (bool, string) {
		c, _ := cardOf("t2")
		return c.Stage == "doing" && c.Holder == nil, fmt.Sprintf("stage %q, holder %v", c.Stage, c.Holder)
	})
	// The start ends at its ready timeout (3 s here); give it room.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && strings.Contains(mustCLI(t, base, "ls"), "api-task-never-comes-up") {
		time.Sleep(300 * time.Millisecond)
	}
	ls = mustCLI(t, base, "ls")
	wts := gitIn(t, here, "worktree", "list")
	ps, _ := exec.Command("ps", "-A", "-o", "command=").Output()
	_, raw = cardOf("t2")
	logged, _ := os.ReadFile(daemonLogOf(t, base))
	var lines []string
	for l := range strings.SplitSeq(string(logged), "\n") {
		if strings.Contains(l, "Task dispatch") || strings.Contains(l, "task t2") {
			lines = append(lines, l)
		}
	}
	note("t2 after its start failed:\n%s\nsessions:\n%s\ngit worktree list:\n%s\ndispatch:\n%s\ndaemon log:\n%s",
		raw, ls, wts, mustCLI(t, base, "task", "dispatch", "-s", "home"), strings.Join(lines, "\n"))
	// Two failures in all: the errored agent's card when its pane let it
	// go, and t2. The block alone is none, as that agent may go on.
	if status := mustCLI(t, base, "task", "dispatch", "-s", "home"); strings.Contains(status, "stopped") {
		t.Errorf("ASSERTION: the board stopped, so one errored agent counted more than one failure:\n%s", status)
	}
	if strings.Contains(ls, "api-task-never-comes-up") {
		t.Errorf("ASSERTION: the session auto made for t2 is still there after its start failed:\n%s", ls)
	}
	if strings.Contains(wts, "task/never-comes-up") {
		t.Errorf("ASSERTION: the worktree auto made for t2 is still there after its start failed:\n%s", wts)
	}
	if strings.Contains(string(ps), "sleep 987") {
		t.Errorf("ASSERTION: the agent auto started for t2 still runs after its start failed")
	}
}
