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

// TestAgentMatcher checks the detection decision: known agent names match, a
// plain shell or unrelated tool does not, a wrapped agent (node running claude)
// matches through its argv, and a user-added name matches.
func TestAgentMatcher(t *testing.T) {
	m := newAgentMatcher([]string{"mycli", "  Spaced-Agent "})
	cases := []struct {
		name string
		comm string
		argv []string
		want bool
	}{
		{"bare claude", "claude", []string{"claude"}, true},
		{"path claude", "claude", []string{"/usr/bin/claude", "--resume"}, true},
		{"codex", "codex", []string{"codex"}, true},
		{"cursor-agent", "cursor-agent", []string{"cursor-agent"}, true},
		{"plain shell", "bash", []string{"-bash"}, false},
		{"unrelated tool", "vim", []string{"vim", "notes.md"}, false},
		{"node wrapper running claude", "node", []string{"node", "/home/u/.npm/claude/cli.js"}, true},
		{"npx opencode", "npx", []string{"npx", "opencode"}, true},
		{"node without agent arg", "node", []string{"node", "server.js"}, false},
		{"user-added name", "mycli", []string{"mycli"}, true},
		{"user-added name case-insensitive", "spaced-agent", []string{"spaced-agent"}, true},
		// A non-interpreter comm that does not match must not be rescued by an
		// incidental argument that happens to share an agent's name.
		{"non-interpreter with agent-like arg", "grep", []string{"grep", "claude", "log.txt"}, false},
	}
	for _, c := range cases {
		if got := m.isAgent(c.comm, c.argv); got != c.want {
			t.Errorf("%s: isAgent(%q, %v) = %v, want %v", c.name, c.comm, c.argv, got, c.want)
		}
	}
}

// TestParseStatTPGID checks the tpgid is read from field 8 even when the comm in
// field 2 contains spaces and parentheses.
func TestParseStatTPGID(t *testing.T) {
	// pid (comm) state ppid pgrp session tty_nr tpgid ...
	line := "1234 (weird (name) x) S 1000 1234 1000 34816 4321 4194304 ..."
	got, ok := parseStatTPGID(line)
	if !ok || got != 4321 {
		t.Fatalf("parseStatTPGID = (%d, %v), want (4321, true)", got, ok)
	}

	if _, ok := parseStatTPGID("garbage without paren"); ok {
		t.Error("parseStatTPGID accepted a line with no ')'")
	}
	if _, ok := parseStatTPGID("1 (init) S 0 1"); ok {
		t.Error("parseStatTPGID accepted a truncated line")
	}
}

// fakeResolver returns a resolve function backed by a per-PTY table, so agent
// detection can be exercised without a real /proc or a real agent process.
type fakeProc struct {
	comm    string
	argv    []string
	running bool
}

func fakeResolver(table map[string]fakeProc) func(string) (string, []string, bool) {
	return func(ptyID string) (string, []string, bool) {
		p, ok := table[ptyID]
		if !ok {
			return "", nil, false
		}
		return p.comm, p.argv, p.running
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

	// Foreground is the shell: nothing happens.
	shell := fakeResolver(map[string]fakeProc{ptyID: {"bash", []string{"-bash"}, true}})
	if n := sess.applyAgentDetection(shell, agent.isAgent); n != 0 {
		t.Fatalf("shell foreground changed %d windows, want 0", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateNone {
		t.Fatalf("state with shell foreground = %q, want none", got)
	}

	// An agent appears: the pane is promoted to working.
	running := fakeResolver(map[string]fakeProc{ptyID: {"claude", []string{"claude"}, true}})
	if n := sess.applyAgentDetection(running, agent.isAgent); n != 1 {
		t.Fatalf("agent appearance changed %d windows, want 1", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateWorking {
		t.Fatalf("state after agent appeared = %q, want working", got)
	}

	// Still running, no change reported and state held.
	if n := sess.applyAgentDetection(running, agent.isAgent); n != 0 {
		t.Fatalf("steady agent changed %d windows, want 0", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateWorking {
		t.Fatalf("state while agent runs = %q, want working", got)
	}

	// Agent exits (foreground back to shell): cleared to none.
	if n := sess.applyAgentDetection(shell, agent.isAgent); n != 1 {
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

	// The user marks the pane needs_input before any agent is detected.
	if err := sess.SetDaemonWindowAgentState(id, AgentStateNeedsInput, "waiting"); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}

	// An agent is in the foreground, but the pane already has a manual state: the
	// detector must not take ownership or overwrite it.
	running := fakeResolver(map[string]fakeProc{ptyID: {"claude", []string{"claude"}, true}})
	if n := sess.applyAgentDetection(running, agent.isAgent); n != 0 {
		t.Fatalf("detection over a manual state changed %d windows, want 0", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateNeedsInput {
		t.Fatalf("manual state = %q, want needs_input (unchanged)", got)
	}

	// The agent then exits: since the detector never owned the window, it leaves
	// the manual state alone.
	shell := fakeResolver(map[string]fakeProc{ptyID: {"bash", []string{"-bash"}, true}})
	if n := sess.applyAgentDetection(shell, agent.isAgent); n != 0 {
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
	running := fakeResolver(map[string]fakeProc{ptyID: {"claude", []string{"claude"}, true}})

	// Detector promotes the pane to working (takes ownership).
	if n := sess.applyAgentDetection(running, agent.isAgent); n != 1 {
		t.Fatalf("promotion changed %d windows, want 1", n)
	}

	// The agent reports needs_input through the verb while still in the foreground.
	if err := sess.SetDaemonWindowAgentState(id, AgentStateNeedsInput, ""); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}

	// A detection tick with the agent still running must not stomp the explicit
	// report back to working.
	if n := sess.applyAgentDetection(running, agent.isAgent); n != 0 {
		t.Fatalf("owned tick over explicit state changed %d windows, want 0", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateNeedsInput {
		t.Fatalf("state while owned = %q, want needs_input", got)
	}

	// When the agent exits, ownership is released and the window clears to none.
	shell := fakeResolver(map[string]fakeProc{ptyID: {"bash", []string{"-bash"}, true}})
	if n := sess.applyAgentDetection(shell, agent.isAgent); n != 1 {
		t.Fatalf("agent exit changed %d windows, want 1", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateNone {
		t.Fatalf("state after owned agent exit = %q, want none", got)
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
	running := fakeResolver(map[string]fakeProc{ptyID: {"claude", []string{"claude"}, true}})
	shell := fakeResolver(map[string]fakeProc{ptyID: {"bash", []string{"-bash"}, true}})

	// Unowned: an output probe must not touch a window the detector never claimed.
	if sess.reconcileAgentOnOutput(ptyID, shell, agent.isAgent) {
		t.Fatal("reconcileAgentOnOutput changed an unowned window")
	}

	// Detector takes ownership of the pane.
	if n := sess.applyAgentDetection(running, agent.isAgent); n != 1 {
		t.Fatalf("promotion changed %d windows, want 1", n)
	}

	// Agent still in the foreground: a probe on output leaves it working.
	if sess.reconcileAgentOnOutput(ptyID, running, agent.isAgent) {
		t.Fatal("reconcileAgentOnOutput cleared a window whose agent is still running")
	}
	if got := agentStateOf(t, sess, id); got != AgentStateWorking {
		t.Fatalf("state while agent runs = %q, want working", got)
	}

	// Agent quits: the very next output probe clears it, no detection poll needed.
	if !sess.reconcileAgentOnOutput(ptyID, shell, agent.isAgent) {
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
	running := fakeResolver(map[string]fakeProc{ptyID: {"claude", []string{"claude"}, true}})

	// The detector finds the agent and promotes the pane.
	if n := sess.applyAgentDetection(running, agent.isAgent); n != 1 {
		t.Fatalf("promotion changed %d windows, want 1", n)
	}

	// The pane goes quiet and the silence timer demotes it to idle.
	const stall = 30 * time.Second
	if n := sess.applyStallHeuristic(time.Now().Add(stall+time.Second), stall, func(string) int64 { return 0 }); n != 1 {
		t.Fatalf("stall heuristic demoted %d windows, want 1", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateIdle {
		t.Fatalf("state after stall = %q, want idle", got)
	}

	// The user sends a prompt: the agent produces output again while still in the
	// foreground. The pane has to go back to working.
	if !sess.reconcileAgentOnOutput(ptyID, running, agent.isAgent) {
		t.Fatal("output from a resumed agent did not change the pane's state")
	}
	if got := agentStateOf(t, sess, id); got != AgentStateWorking {
		t.Fatalf("state after the agent resumed = %q, want working", got)
	}

	// A detection poll must not undo the resume.
	if n := sess.applyAgentDetection(running, agent.isAgent); n != 0 {
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
	running := fakeResolver(map[string]fakeProc{ptyID: {"claude", []string{"claude"}, true}})

	if n := sess.applyAgentDetection(running, agent.isAgent); n != 1 {
		t.Fatalf("promotion changed %d windows, want 1", n)
	}
	if err := sess.SetDaemonWindowAgentState(id, AgentStateNeedsInput, "approve?"); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}
	if sess.reconcileAgentOnOutput(ptyID, running, agent.isAgent) {
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
	shell := fakeResolver(map[string]fakeProc{ptyID: {"bash", []string{"-bash"}, true}})

	if err := sess.SetDaemonWindowAgentState(id, AgentStateNeedsInput, "waiting"); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}
	if sess.reconcileAgentOnOutput(ptyID, shell, agent.isAgent) {
		t.Fatal("reconcileAgentOnOutput cleared a manual state")
	}
	if got := agentStateOf(t, sess, id); got != AgentStateNeedsInput {
		t.Fatalf("manual state after probe = %q, want needs_input", got)
	}
}

// TestResolveAgentDetectInterval checks the config/env/default precedence and the
// disable paths for the auto-detector's poll interval.
func TestResolveAgentDetectInterval(t *testing.T) {
	t.Setenv("TUIOS_AGENT_DETECT_SECONDS", "")

	disabled := false
	if got := resolveAgentDetectInterval(&disabled, 5*time.Second); got != 0 {
		t.Errorf("explicit disable = %v, want 0", got)
	}
	enabled := true
	if got := resolveAgentDetectInterval(&enabled, 5*time.Second); got != 5*time.Second {
		t.Errorf("enabled with config = %v, want 5s", got)
	}
	if got := resolveAgentDetectInterval(nil, 3*time.Second); got != 3*time.Second {
		t.Errorf("positive config = %v, want 3s", got)
	}
	if got := resolveAgentDetectInterval(nil, -1); got != 0 {
		t.Errorf("negative config = %v, want 0 (disabled)", got)
	}
	if got := resolveAgentDetectInterval(nil, 0); got != defaultAgentDetectInterval {
		t.Errorf("zero config = %v, want default", got)
	}

	t.Setenv("TUIOS_AGENT_DETECT_SECONDS", "7")
	if got := resolveAgentDetectInterval(nil, 0); got != 7*time.Second {
		t.Errorf("env override = %v, want 7s", got)
	}
	t.Setenv("TUIOS_AGENT_DETECT_SECONDS", "0")
	if got := resolveAgentDetectInterval(nil, 0); got != 0 {
		t.Errorf("env disable = %v, want 0", got)
	}

	// The autodetect env toggle disables detection when the config leaves it unset.
	t.Setenv("TUIOS_AGENT_DETECT_SECONDS", "")
	t.Setenv("TUIOS_AGENT_AUTODETECT", "off")
	if got := resolveAgentDetectInterval(nil, 3*time.Second); got != 0 {
		t.Errorf("env autodetect off = %v, want 0", got)
	}
	// An explicit config enable overrides the env toggle.
	enabledAgain := true
	if got := resolveAgentDetectInterval(&enabledAgain, 3*time.Second); got != 3*time.Second {
		t.Errorf("config enable over env off = %v, want 3s", got)
	}
}

// TestResolveAgentBinaries checks the config list and the env override merge, and
// that blanks are ignored.
func TestResolveAgentBinaries(t *testing.T) {
	t.Setenv("TUIOS_AGENT_BINARIES", " extra1 , ,extra2 ")
	got := resolveAgentBinaries([]string{"cfg1", " "})
	want := map[string]bool{"cfg1": true, "extra1": true, "extra2": true}
	seen := map[string]bool{}
	for _, n := range got {
		seen[n] = true
	}
	for w := range want {
		if !seen[w] {
			t.Errorf("resolveAgentBinaries missing %q, got %v", w, got)
		}
	}
	// The merged matcher must recognise a config-added name.
	m := newAgentMatcher(got)
	if !m.isAgent("cfg1", []string{"cfg1"}) {
		t.Error("matcher did not recognise config-added name cfg1")
	}
}
