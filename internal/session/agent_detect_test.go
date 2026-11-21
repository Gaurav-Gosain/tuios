package session

import (
	"testing"
	"time"
)

// TestAgentBaseName checks the reduction of a comm or argv token to the base name
// used for matching: directories, a login-shell '-' prefix, script extensions and
// a trailing NUL are all stripped, and the result is lowercased.
func TestAgentBaseName(t *testing.T) {
	cases := map[string]string{
		"claude":                   "claude",
		"/usr/local/bin/claude":    "claude",
		"Claude":                   "claude",
		"claude.js":                "claude",
		"/opt/agents/opencode.mjs": "opencode",
		"-bash":                    "bash",
		"node\x00":                 "node",
		"  aider  ":                "aider",
		"":                         "",
	}
	for in, want := range cases {
		if got := agentBaseName(in); got != want {
			t.Errorf("agentBaseName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestAgentMatcher checks the detection decision across the shapes a harness
// actually launches in: a native binary, a versioned binary behind a renamed
// process, the interpreter wrappers npm and pip installs produce, and the
// non-agent cases that must keep reading as non-agents.
func TestAgentMatcher(t *testing.T) {
	m := newAgentMatcher([]string{"mycli", "  Spaced-Agent "})
	cases := []struct {
		name string
		info foregroundInfo
		want bool
	}{
		{"bare claude", foregroundInfo{comm: "claude", argv: []string{"claude"}}, true},
		{"path claude", foregroundInfo{comm: "claude", argv: []string{"/usr/bin/claude", "--resume"}}, true},
		{"codex", foregroundInfo{comm: "codex", argv: []string{"codex"}}, true},
		{"cursor-agent", foregroundInfo{comm: "cursor-agent", argv: []string{"cursor-agent"}}, true},
		{"plain shell", foregroundInfo{comm: "bash", argv: []string{"-bash"}}, false},
		{"unrelated tool", foregroundInfo{comm: "vim", argv: []string{"vim", "notes.md"}}, false},
		{"node wrapper running claude", foregroundInfo{comm: "node", argv: []string{"node", "/usr/lib/node_modules/claude/cli.js"}}, true},
		{"node running a script in a checkout named claude", foregroundInfo{comm: "node", argv: []string{"node", "/home/u/dev/claude/cli.js"}}, false},
		{"npx opencode", foregroundInfo{comm: "npx", argv: []string{"npx", "opencode"}}, true},
		{"node without agent arg", foregroundInfo{comm: "node", argv: []string{"node", "server.js"}}, false},
		{"user-added name", foregroundInfo{comm: "mycli", argv: []string{"mycli"}}, true},
		{"user-added name case-insensitive", foregroundInfo{comm: "spaced-agent", argv: []string{"spaced-agent"}}, true},
		{"non-interpreter with agent-like arg", foregroundInfo{comm: "grep", argv: []string{"grep", "claude", "log.txt"}}, false},

		{
			"versioned binary named only by its directory",
			foregroundInfo{
				comm: "2.1.222",
				argv: []string{"claude", "--resume"},
				exe:  "/home/u/.local/share/claude/versions/2.1.222",
			},
			true,
		},
		{
			"comm truncated at fifteen characters",
			foregroundInfo{comm: "octofriend-cli-", argv: []string{"octofriend"}, exe: "/usr/local/bin/octofriend"},
			true,
		},
		{
			"platform package binary",
			foregroundInfo{
				comm: "droid",
				argv: []string{"droid"},
				exe:  "/home/u/n/node_modules/@factory/cli-linux-x64/bin/droid",
			},
			true,
		},
		{
			"python running aider",
			foregroundInfo{comm: "python3", argv: []string{"python3", "/usr/lib/py/site-packages/aider/main.py"}, exe: "/usr/bin/python3.13"},
			true,
		},
		{
			"bun running an agent script",
			foregroundInfo{comm: "bun", argv: []string{"bun", "/home/u/.bun/install/global/node_modules/opencode/index.ts"}, exe: "/home/u/.bun/bin/bun"},
			true,
		},
		{
			"renamed wrapper over a shell",
			foregroundInfo{comm: "my-launcher", argv: []string{"my-launcher", "codex"}, exe: "/usr/bin/bash"},
			true,
		},
		{
			"python running something else",
			foregroundInfo{comm: "python3", argv: []string{"python3", "manage.py", "runserver"}, exe: "/usr/bin/python3.13"},
			false,
		},
		{
			"unrelated binary with a clean path",
			foregroundInfo{comm: "htop", argv: []string{"htop"}, exe: "/usr/bin/htop"},
			false,
		},
	}
	for _, c := range cases {
		if got := m.isAgent(c.info); got != c.want {
			t.Errorf("%s: isAgent(%+v) = %v, want %v", c.name, c.info, got, c.want)
		}
	}
}

type fakeProc struct {
	info    foregroundInfo
	running bool
}

func fakeResolver(table map[string]fakeProc) func(string) (foregroundInfo, bool) {
	return func(ptyID string) (foregroundInfo, bool) {
		p, ok := table[ptyID]
		if !ok {
			return foregroundInfo{}, false
		}
		return p.info, p.running
	}
}

func ptyIDOfWindow(t *testing.T, sess *Session, windowID string) string {
	t.Helper()
	for _, w := range sess.GetState().Windows {
		if w.ID == windowID {
			return w.PTYID
		}
	}
	t.Fatalf("window %s not found", windowID)
	return ""
}

// TestAgentDetectionPromotesAndClears checks the core lifecycle: an agent
// appearing in the foreground promotes a fresh pane to working, and the agent
// leaving clears it back to none. Re-running with no change reports zero.
func TestAgentDetectionPromotesAndClears(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	ptyID := ptyIDOfWindow(t, sess, id)
	agent := newAgentMatcher(nil)

	shell := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{comm: "bash", argv: []string{"-bash"}}, true}})
	if n := sess.applyAgentDetection(shell, agent.identifyDetail); n != 0 {
		t.Fatalf("shell foreground changed %d windows, want 0", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateNone {
		t.Fatalf("state with shell foreground = %q, want none", got)
	}

	running := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{comm: "claude", argv: []string{"claude"}}, true}})
	if n := sess.applyAgentDetection(running, agent.identifyDetail); n != 1 {
		t.Fatalf("agent appearance changed %d windows, want 1", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateWorking {
		t.Fatalf("state after agent appeared = %q, want working", got)
	}

	if n := sess.applyAgentDetection(running, agent.identifyDetail); n != 0 {
		t.Fatalf("steady agent changed %d windows, want 0", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateWorking {
		t.Fatalf("state while agent runs = %q, want working", got)
	}

	if n := sess.applyAgentDetection(shell, agent.identifyDetail); n != 1 {
		t.Fatalf("agent exit changed %d windows, want 1", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateNone {
		t.Fatalf("state after agent exit = %q, want none", got)
	}
}

// TestAgentDetectionNeverClobbersManual checks a state a user set through
// set-agent-state is never overwritten by auto-detection, whether the pane runs
// an agent or not.
func TestAgentDetectionNeverClobbersManual(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	ptyID := ptyIDOfWindow(t, sess, id)
	agent := newAgentMatcher(nil)

	if err := sess.SetDaemonWindowAgentState(id, AgentStateNeedsInput, "waiting"); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}

	running := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{comm: "claude", argv: []string{"claude"}}, true}})
	if n := sess.applyAgentDetection(running, agent.identifyDetail); n != 0 {
		t.Fatalf("detection over a manual state changed %d windows, want 0", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateNeedsInput {
		t.Fatalf("manual state = %q, want needs_input (unchanged)", got)
	}

	shell := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{comm: "bash", argv: []string{"-bash"}}, true}})
	if n := sess.applyAgentDetection(shell, agent.identifyDetail); n != 0 {
		t.Fatalf("agent exit over a manual state changed %d windows, want 0", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateNeedsInput {
		t.Fatalf("manual state after agent exit = %q, want needs_input", got)
	}
}

// TestAgentDetectionYieldsToExplicitWhileOwned checks that once the detector owns
// a window, an explicit set-agent-state during the agent's run wins and is not
// re-asserted, and the state is only cleared when the agent finally exits.
func TestAgentDetectionYieldsToExplicitWhileOwned(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	ptyID := ptyIDOfWindow(t, sess, id)
	agent := newAgentMatcher(nil)
	running := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{comm: "claude", argv: []string{"claude"}}, true}})

	if n := sess.applyAgentDetection(running, agent.identifyDetail); n != 1 {
		t.Fatalf("promotion changed %d windows, want 1", n)
	}

	if err := sess.SetDaemonWindowAgentState(id, AgentStateNeedsInput, ""); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}

	if n := sess.applyAgentDetection(running, agent.identifyDetail); n != 0 {
		t.Fatalf("owned tick over explicit state changed %d windows, want 0", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateNeedsInput {
		t.Fatalf("state while owned = %q, want needs_input", got)
	}

	shell := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{comm: "bash", argv: []string{"-bash"}}, true}})
	if n := sess.applyAgentDetection(shell, agent.identifyDetail); n != 1 {
		t.Fatalf("agent exit changed %d windows, want 1", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateNone {
		t.Fatalf("state after owned agent exit = %q, want none", got)
	}
}

// TestAgentDetectionRestoresHarnessAfterProgramReset checks that a detector-owned
// pane regains its harness after program status clears (OSC 7501) and then returns
// to idle. Codex does this when opening and closing an external editor while its
// process stays alive.
func TestAgentDetectionRestoresHarnessAfterProgramReset(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	ptyID := ptyIDOfWindow(t, sess, id)
	agent := newAgentMatcher(nil)
	running := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{comm: "codex", argv: []string{"codex"}}, true}})
	sess.applyAgentDetection(running, agent.identifyDetail)
	if _, applied, err := sess.ApplyAgentReport(id, AgentReport{State: AgentStateNone, Source: AgentSourceProgram}); err != nil || !applied {
		t.Fatalf("program report none: applied=%v, err=%v", applied, err)
	}
	if got := agentHarnessIDOf(t, sess, id); got != "" {
		t.Fatalf("harness after none = %q, want empty", got)
	}
	if _, applied, err := sess.ApplyAgentReport(id, AgentReport{State: AgentStateIdle, Source: AgentSourceProgram}); err != nil || !applied {
		t.Fatalf("program report idle: applied=%v, err=%v", applied, err)
	}
	if n := sess.applyAgentDetection(running, agent.identifyDetail); n != 1 {
		t.Fatalf("restoring harness changed %d windows, want 1", n)
	}
	if got := agentHarnessIDOf(t, sess, id); got != "codex" {
		t.Fatalf("harness = %q, want codex", got)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateIdle {
		t.Fatalf("state = %q, want idle", got)
	}
}

// TestClearExitedAgent proves the output-driven clear removes an auto-detected
// glyph the moment the foreground returns to the shell, with no poll in between,
// while leaving an unowned window, a still-running agent, and a manual state
// untouched.
func TestClearExitedAgent(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	ptyID := ptyIDOfWindow(t, sess, id)
	agent := newAgentMatcher(nil)
	running := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{comm: "claude", argv: []string{"claude"}}, true}})
	shell := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{comm: "bash", argv: []string{"-bash"}}, true}})

	if sess.reconcileAgentOnOutput(ptyID, shell, agent.identifyDetail) {
		t.Fatal("reconcileAgentOnOutput changed an unowned window")
	}

	if n := sess.applyAgentDetection(running, agent.identifyDetail); n != 1 {
		t.Fatalf("promotion changed %d windows, want 1", n)
	}

	if sess.reconcileAgentOnOutput(ptyID, running, agent.identifyDetail) {
		t.Fatal("reconcileAgentOnOutput cleared a window whose agent is still running")
	}
	if got := agentStateOf(t, sess, id); got != AgentStateWorking {
		t.Fatalf("state while agent runs = %q, want working", got)
	}

	if !sess.reconcileAgentOnOutput(ptyID, shell, agent.identifyDetail) {
		t.Fatal("reconcileAgentOnOutput did not clear after the agent left the foreground")
	}
	if got := agentStateOf(t, sess, id); got != AgentStateNone {
		t.Fatalf("state after agent exit = %q, want none", got)
	}
}

// TestAgentResumesAfterStall is the regression for an agent whose indicator
// latched: once the silence timer demoted a detected agent to idle, nothing in
// the daemon could ever move it back to working, so a pane running a coding
// agent showed idle for the rest of its life no matter how hard the agent then
// worked. Output from a pane whose agent is still in the foreground is the
// signal that it resumed.
func TestAgentResumesAfterStall(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	ptyID := ptyIDOfWindow(t, sess, id)
	agent := newAgentMatcher(nil)
	running := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{comm: "claude", argv: []string{"claude"}}, true}})

	if n := sess.applyAgentDetection(running, agent.identifyDetail); n != 1 {
		t.Fatalf("promotion changed %d windows, want 1", n)
	}

	const stall = 30 * time.Second
	if n := sess.applyStallHeuristic(time.Now().Add(stall+time.Second), stall, func(string) int64 { return 0 }, nil); n != 1 {
		t.Fatalf("stall heuristic demoted %d windows, want 1", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateIdle {
		t.Fatalf("state after stall = %q, want idle", got)
	}

	if !sess.reconcileAgentOnOutput(ptyID, running, agent.identifyDetail) {
		t.Fatal("output from a resumed agent did not change the pane's state")
	}
	if got := agentStateOf(t, sess, id); got != AgentStateWorking {
		t.Fatalf("state after the agent resumed = %q, want working", got)
	}
	if got := agentHarnessIDOf(t, sess, id); got != "claude-code" {
		t.Fatalf("harness after the agent resumed = %q, want claude-code", got)
	}

	if n := sess.applyAgentDetection(running, agent.identifyDetail); n != 0 {
		t.Fatalf("detection poll after resume changed %d windows, want 0", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateWorking {
		t.Fatalf("state after a poll following resume = %q, want working", got)
	}
}

// TestAgentResumeRespectsHigherSource proves the resume never overwrites a state
// a harness reported for itself: a pane that reported needs_input keeps saying
// needs_input however much output it produces, since a report outranks the
// detector.
func TestAgentResumeRespectsHigherSource(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	ptyID := ptyIDOfWindow(t, sess, id)
	agent := newAgentMatcher(nil)
	running := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{comm: "claude", argv: []string{"claude"}}, true}})

	if n := sess.applyAgentDetection(running, agent.identifyDetail); n != 1 {
		t.Fatalf("promotion changed %d windows, want 1", n)
	}
	if err := sess.SetDaemonWindowAgentState(id, AgentStateNeedsInput, "approve?"); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}
	if sess.reconcileAgentOnOutput(ptyID, running, agent.identifyDetail) {
		t.Fatal("output overwrote a reported needs_input")
	}
	if got := agentStateOf(t, sess, id); got != AgentStateNeedsInput {
		t.Fatalf("reported state after output = %q, want needs_input", got)
	}
}

// TestClearExitedAgentRespectsManual proves the output-driven clear never touches
// a state a user set through set-agent-state, since the detector does not own it.
func TestClearExitedAgentRespectsManual(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	ptyID := ptyIDOfWindow(t, sess, id)
	agent := newAgentMatcher(nil)
	shell := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{comm: "bash", argv: []string{"-bash"}}, true}})

	if err := sess.SetDaemonWindowAgentState(id, AgentStateNeedsInput, "waiting"); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}
	if sess.reconcileAgentOnOutput(ptyID, shell, agent.identifyDetail) {
		t.Fatal("reconcileAgentOnOutput cleared a manual state")
	}
	if got := agentStateOf(t, sess, id); got != AgentStateNeedsInput {
		t.Fatalf("manual state after probe = %q, want needs_input", got)
	}
}

// TestAnAgentThatReportedAndThenQuitLosesItsRow.
//
// Reported as: launched an agent, quit it, and the agent list still showed the
// pane, with the harness it used to be running.
//
// Most agents report their own state rather than waiting to be detected, so
// the detector never takes the claim and its clearing branch never runs. The
// reporter is the agent, and an agent that has exited cannot retract anything,
// so the last thing it said stood for as long as the pane lived. The stall
// timer demoted it to unknown, which is a state, so the pane stayed in the
// list.
//
// Negative control: without the shell case in applyAgentDetection the pane
// keeps its state and its harness here.
func TestAnAgentThatReportedAndThenQuitLosesItsRow(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	ptyID := ptyIDOfWindow(t, sess, id)
	agent := newAgentMatcher(nil)

	if _, _, err := sess.ApplyAgentReport(id, AgentReport{
		State: AgentStateWorking, Source: AgentSourceOSC, Harness: "claude-code",
	}); err != nil {
		t.Fatalf("ApplyAgentReport: %v", err)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateWorking {
		t.Fatalf("ASSERTION: the pane is %q, not working, so there is nothing to clear", got)
	}

	agentRunning := fakeResolver(map[string]fakeProc{
		ptyID: {foregroundInfo{comm: "claude", argv: []string{"claude"}, pid: 200, shellPID: 100}, true},
	})
	sess.applyAgentDetection(agentRunning, agent.identifyDetail)

	shell := fakeResolver(map[string]fakeProc{
		ptyID: {foregroundInfo{comm: "fish", argv: []string{"fish"}, pid: 100, shellPID: 100}, true},
	})
	sess.applyAgentDetection(shell, agent.identifyDetail)

	if got := agentStateOf(t, sess, id); got != AgentStateNone {
		t.Errorf("the pane is still %q after the agent quit", got)
	}
	for _, w := range sess.GetState().Windows {
		if w.ID == id && w.AgentHarness != "" {
			t.Errorf("the pane still claims harness %q", w.AgentHarness)
		}
	}
}

// TestAPaneRunningSomethingElseKeepsItsState. An agent that opened an editor
// is still an agent, which is the same argument the miss count makes for a
// claim the detector does own.
func TestAPaneRunningSomethingElseKeepsItsState(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	ptyID := ptyIDOfWindow(t, sess, id)
	agent := newAgentMatcher(nil)

	if _, _, err := sess.ApplyAgentReport(id, AgentReport{
		State: AgentStateWorking, Source: AgentSourceOSC, Harness: "claude-code",
	}); err != nil {
		t.Fatalf("ApplyAgentReport: %v", err)
	}

	agentRunning := fakeResolver(map[string]fakeProc{
		ptyID: {foregroundInfo{comm: "claude", argv: []string{"claude"}, pid: 200, shellPID: 100}, true},
	})
	sess.applyAgentDetection(agentRunning, agent.identifyDetail)

	editor := fakeResolver(map[string]fakeProc{
		ptyID: {foregroundInfo{comm: "nvim", argv: []string{"nvim"}, pid: 200, shellPID: 100}, true},
	})
	sess.applyAgentDetection(editor, agent.identifyDetail)

	if got := agentStateOf(t, sess, id); got != AgentStateWorking {
		t.Errorf("a pane running an editor lost its state: %q", got)
	}
}

// TestAPaneThatNeverRanAnAgentKeepsItsState.
//
// An unhooked harness is exercised by setting a low-ranked claim on a pane and
// letting a screen rule read what it paints, with no agent binary anywhere.
// The sweep that clears a stale agent must not clear that: the state is not
// stale, it is the only thing anybody ever said about the pane.
//
// Negative control: dropping the sawProcess condition clears it here, which is
// the end-to-end test this broke.
func TestAPaneThatNeverRanAnAgentKeepsItsState(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	ptyID := ptyIDOfWindow(t, sess, id)
	agent := newAgentMatcher(nil)

	if _, _, err := sess.ApplyAgentReport(id, AgentReport{
		State: AgentStateWorking, Source: AgentSourceStall, Harness: "claude-code",
	}); err != nil {
		t.Fatalf("ApplyAgentReport: %v", err)
	}

	shell := fakeResolver(map[string]fakeProc{
		ptyID: {foregroundInfo{comm: "fish", argv: []string{"fish"}, pid: 100, shellPID: 100}, true},
	})
	sess.applyAgentDetection(shell, agent.identifyDetail)

	if got := agentStateOf(t, sess, id); got != AgentStateWorking {
		t.Errorf("a pane that never ran an agent lost its state: %q", got)
	}
}
