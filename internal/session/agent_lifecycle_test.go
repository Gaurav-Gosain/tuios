package session

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// TestUnhookedAgentLifecycleAnswersBlockedOrThinking walks a harness with no
// hooks through the one sequence that matters to a person: it starts, it
// paints a permission prompt and goes silent, the person answers, it works
// again, and it finishes. At every step the daemon has to say whether the pane
// needs a person, and a wrong answer in either direction is the bug this test
// exists for: "blocked" over a pane that is thinking sends the person to look
// for nothing, and "working" over a pane that is blocked leaves them waiting.
func TestUnhookedAgentLifecycleAnswersBlockedOrThinking(t *testing.T) {
	reg, errs := harness.Load()
	if len(errs) != 0 {
		t.Fatalf("loading the bundled manifests: %v", errs)
	}
	sess, winID := bareSessionWithWindow(t)
	ptyID := ptyIDOfWindow(t, sess, winID)
	agent := newAgentMatcher(nil)
	running := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{
		comm: "claude", argv: []string{"claude"},
		exe: "/home/u/.local/share/claude/versions/2.1.266",
	}, true}})
	shell := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{comm: "fish", argv: []string{"-fish"}}, true}})
	look := func(id string) bool { return sess.scanScreenForAgent(id, reg) }
	const stall = 30 * time.Second

	// 1. The agent starts. The detector sees it and says working.
	if n := sess.applyAgentDetection(running, agent.identifyDetail); n != 1 {
		t.Fatalf("start: detection changed %d windows, want 1", n)
	}
	assertAttention(t, sess, winID, AgentStateWorking, false)

	// 2. It paints a permission prompt and goes silent. The screen tier reads
	// the prompt: the pane needs a person.
	paintPane(t, sess.GetPTY(ptyID), "\x1b[2J\x1b[H"+claudePermissionPrompt)
	if !look(ptyID) {
		t.Fatal("prompt: the screen tier did not match the permission prompt")
	}
	assertAttention(t, sess, winID, AgentStateNeedsInput, true)
	if msg := agentMessageOf(t, sess, winID); msg == "" {
		t.Error("prompt: the blocked state carries no reason")
	}

	// 3. The person answers. The agent paints over the prompt and works. The
	// pane must stop saying it needs a person, at once, on the look the repaint
	// runs: a person told "blocked" about a pane that is thinking is the exact
	// mistake this feature exists to prevent.
	paintPane(t, sess.GetPTY(ptyID), "\x1b[2J\x1b[H"+"Running tests...\r\n")
	if look(ptyID) {
		t.Fatal("answered: a screen with no prompt on it still matched a rule")
	}
	assertAttention(t, sess, winID, AgentStateWorking, false)
	if src := sess.agentClaimFor(winID).source; src != AgentSourceDetect {
		t.Fatalf("answered: claim source = %q, want the detector's claim back", src)
	}

	// 4. It finishes its turn and sits at its own prompt, silent. No rule knows
	// that screen. The silence timer must not call it idle, because idle says
	// nothing needs you and nothing here knows that: it says unknown.
	paintPane(t, sess.GetPTY(ptyID), "\x1b[2J\x1b[H"+"> \r\n")
	backdateAgentClaim(t, sess, winID, stall+time.Second)
	if n := sess.applyStallHeuristic(time.Now(), stall, func(string) int64 { return 0 }, look); n != 1 {
		t.Fatalf("silence: demoted %d panes, want 1", n)
	}
	assertAttention(t, sess, winID, AgentStateUnknown, false)
	if src := sess.agentClaimFor(winID).source; src != AgentSourceStall {
		t.Fatalf("silence: claim source = %q, want stall", src)
	}

	// 5. The person types a new prompt and the agent works again. Output from
	// the agent's own process takes the pane back from the silence timer.
	if !sess.reconcileAgentOnOutput(ptyID, running, agent.identifyDetail) {
		t.Fatal("resume: output from the agent did not move the pane back to working")
	}
	assertAttention(t, sess, winID, AgentStateWorking, false)

	// 6. The agent exits and the shell prompt returns. The pane is no agent.
	if !sess.reconcileAgentOnOutput(ptyID, shell, agent.identifyDetail) {
		t.Fatal("exit: the shell's output did not clear the pane")
	}
	assertAttention(t, sess, winID, AgentStateNone, false)
}

// TestDetectorDoesNotDropAnAgentOnOneMissedRead pins the hysteresis: an agent
// that hands its terminal to another program for a moment (an editor it opened,
// a pager) is not an agent that exited. The claim clears when the pane's own
// shell is back in the foreground, or after agentDetectMissLimit consecutive
// ticks of something else, never on a single miss.
func TestDetectorDoesNotDropAnAgentOnOneMissedRead(t *testing.T) {
	sess, winID := bareSessionWithWindow(t)
	ptyID := ptyIDOfWindow(t, sess, winID)
	agent := newAgentMatcher(nil)
	running := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{
		comm: "crush", argv: []string{"crush"}, exe: "/usr/bin/crush", pid: 200, shellPID: 100,
	}, true}})
	editor := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{
		comm: "vim", argv: []string{"vim", "notes.md"}, exe: "/usr/bin/vim", pid: 300, shellPID: 100,
	}, true}})
	shell := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{
		comm: "fish", argv: []string{"-fish"}, exe: "/usr/bin/fish", pid: 100, shellPID: 100,
	}, true}})

	if n := sess.applyAgentDetection(running, agent.identifyDetail); n != 1 {
		t.Fatalf("promotion changed %d windows, want 1", n)
	}
	// The agent opens an editor: one tick, then several more, all miss.
	for i := 1; i < agentDetectMissLimit; i++ {
		if n := sess.applyAgentDetection(editor, agent.identifyDetail); n != 0 {
			t.Fatalf("miss %d: detection changed %d windows, want the claim kept", i, n)
		}
		if got := agentStateOf(t, sess, winID); got != AgentStateWorking {
			t.Fatalf("miss %d: state = %q, want working kept", i, got)
		}
	}
	// The agent comes back: the count resets.
	if n := sess.applyAgentDetection(running, agent.identifyDetail); n != 0 {
		t.Fatalf("return: detection changed %d windows, want 0", n)
	}
	if misses := sess.agentClaimFor(winID).misses; misses != 0 {
		t.Fatalf("return: misses = %d, want 0", misses)
	}
	// The editor stays for the full limit: the agent is gone for good.
	for i := 0; i < agentDetectMissLimit-1; i++ {
		sess.applyAgentDetection(editor, agent.identifyDetail)
	}
	if got := agentStateOf(t, sess, winID); got != AgentStateWorking {
		t.Fatalf("one short of the limit: state = %q, want working kept", got)
	}
	if n := sess.applyAgentDetection(editor, agent.identifyDetail); n != 1 {
		t.Fatalf("at the limit: detection changed %d windows, want the claim cleared", n)
	}
	assertAttention(t, sess, winID, AgentStateNone, false)

	// The shell coming back clears at once, with no count.
	if n := sess.applyAgentDetection(running, agent.identifyDetail); n != 1 {
		t.Fatalf("second promotion changed %d windows, want 1", n)
	}
	if n := sess.applyAgentDetection(shell, agent.identifyDetail); n != 1 {
		t.Fatalf("shell back: detection changed %d windows, want the claim cleared", n)
	}
	assertAttention(t, sess, winID, AgentStateNone, false)
}

// TestOutputProbeClearsOnlyWhenTheShellIsBack pins the same rule on the
// output-driven path: a pane whose agent handed off to another program keeps
// its claim until the tick counter decides, but the shell prompt returning is
// the agent's exit and clears at once.
func TestOutputProbeClearsOnlyWhenTheShellIsBack(t *testing.T) {
	sess, winID := bareSessionWithWindow(t)
	ptyID := ptyIDOfWindow(t, sess, winID)
	agent := newAgentMatcher(nil)
	running := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{
		comm: "crush", argv: []string{"crush"}, exe: "/usr/bin/crush", pid: 200, shellPID: 100,
	}, true}})
	pager := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{
		comm: "less", argv: []string{"less"}, exe: "/usr/bin/less", pid: 300, shellPID: 100,
	}, true}})
	shell := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{
		comm: "fish", argv: []string{"-fish"}, exe: "/usr/bin/fish", pid: 100, shellPID: 100,
	}, true}})

	if n := sess.applyAgentDetection(running, agent.identifyDetail); n != 1 {
		t.Fatalf("promotion changed %d windows, want 1", n)
	}
	if sess.reconcileAgentOnOutput(ptyID, pager, agent.identifyDetail) {
		t.Fatal("a pager in the foreground cleared the agent on one probe")
	}
	if got := agentStateOf(t, sess, winID); got != AgentStateWorking {
		t.Fatalf("state with a pager up = %q, want working kept", got)
	}
	if !sess.reconcileAgentOnOutput(ptyID, shell, agent.identifyDetail) {
		t.Fatal("the shell prompt returning did not clear the agent")
	}
	assertAttention(t, sess, winID, AgentStateNone, false)
}

// assertAttention checks a window's state and the one-bit answer derived from
// it, since the bit is what a consumer reads.
func assertAttention(t *testing.T, sess *Session, winID string, want AgentState, needsYou bool) {
	t.Helper()
	got := agentStateOf(t, sess, winID)
	if got != want {
		t.Fatalf("state = %q, want %q", got, want)
	}
	if got.NeedsYou() != needsYou {
		t.Fatalf("state %q: NeedsYou = %v, want %v", got, got.NeedsYou(), needsYou)
	}
}

func agentMessageOf(t *testing.T, sess *Session, winID string) string {
	t.Helper()
	for _, w := range sess.GetState().Windows {
		if w.ID == winID {
			return w.AgentMessage
		}
	}
	t.Fatalf("window %s not found", winID)
	return ""
}

// TestAReportNamingItsHarnessIsCertain pins the top of the identity ranking:
// the detector's name match is strong, and the harness naming itself through a
// report is certain, which is what the hook shim does with --harness. A report
// that names no harness leaves the detector's evidence standing.
func TestAReportNamingItsHarnessIsCertain(t *testing.T) {
	sess, winID := bareSessionWithWindow(t)
	ptyID := ptyIDOfWindow(t, sess, winID)
	agent := newAgentMatcher(nil)
	running := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{comm: "claude", argv: []string{"claude"}}, true}})
	if n := sess.applyAgentDetection(running, agent.identifyDetail); n != 1 {
		t.Fatalf("promotion changed %d windows, want 1", n)
	}
	if claim := sess.agentClaimFor(winID); claim.identity != identityManifest || claim.identity.confidence() != "strong" {
		t.Fatalf("detector claim identity = %q (%s), want manifest (strong)", claim.identity, claim.identity.confidence())
	}

	// A hook reporting a state alone does not change what named the harness.
	if _, _, err := sess.ApplyAgentReport(winID, AgentReport{State: AgentStateWorking}); err != nil {
		t.Fatal(err)
	}
	if claim := sess.agentClaimFor(winID); claim.identity != identityManifest {
		t.Fatalf("after a report with no harness, identity = %q, want manifest kept", claim.identity)
	}

	// The hook naming its harness is the harness speaking for itself.
	if _, _, err := sess.ApplyAgentReport(winID, AgentReport{State: AgentStateNeedsInput, Harness: "claude-code"}); err != nil {
		t.Fatal(err)
	}
	claim := sess.agentClaimFor(winID)
	if claim.identity != identityReport || claim.identity.confidence() != "certain" {
		t.Fatalf("after a report naming its harness, identity = %q (%s), want report (certain)", claim.identity, claim.identity.confidence())
	}
}
