package tuie2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// heldHook is a tuios agent-hook run the way Claude Code runs it, for the
// agent's pane, and what it printed.
type heldHook struct {
	stdout bytes.Buffer
	exited chan error
}

// startHeldHook starts the hook with payload and returns once it runs. The
// test's cleanup kills it.
func startHeldHook(t *testing.T, base, payload string) *heldHook {
	t.Helper()
	cmd := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "e2e-agent", "--window", "0")
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	cmd.Stdin = strings.NewReader(payload)
	h := &heldHook{exited: make(chan error, 1)}
	cmd.Stdout = &h.stdout
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the hook: %v", err)
	}
	finished := make(chan struct{})
	go func() {
		h.exited <- cmd.Wait()
		close(finished)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-finished
	})
	return h
}

// approvalsClient starts a daemon with approvals on for claude-code, an
// agent's session nobody looks at, and a client attached to another session
// in window management mode.
func approvalsClient(t *testing.T) (string, *tuitest.Terminal) {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	dir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	body := "[agents.approvals]\nenabled = [\"claude-code\"]\nhold_seconds = 60\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	for _, name := range []string{"e2e-home", "e2e-agent"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", name, err, out)
		}
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-home"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("normalise to window mode: %v", err)
	}
	if err := term.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("client never settled in window management mode: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)
	return base, term
}

// TestInboxRiskyApprovalTakesTwoPresses is the risk rule round trip with the
// real hook: a PermissionRequest for rm -rf build/ is held, the Inbox marks it
// risky and names the rule, one 1 answers nothing and says what a second
// does, and the second 1 makes the hook print Claude Code's allow.
//
// Negative control: with the risk arm removed from inboxApprovalGate, the
// first 1 answers and the hook returns before the second.
func TestInboxRiskyApprovalTakesTwoPresses(t *testing.T) {
	base, term := approvalsClient(t)
	hook := startHeldHook(t, base, `{"hook_event_name":"PermissionRequest","session_id":"e2e-risk","tool_name":"Bash","tool_input":{"command":"rm -rf build/"}}`)

	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Approvals 1", "[1/3] risky: approve Bash: rm -r", "Risky: recursive delete", "n deny with reason")
	}, uiTimeout); err != nil {
		t.Fatalf("the Inbox never marked the approval risky: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "inbox-risky-held")
	time.Sleep(answerSettle)

	if err := term.SendKeys("1"); err != nil {
		t.Fatalf("first press: %v", err)
	}
	if err := term.WaitForText("Press 1 again to allow rm -rf build/", uiTimeout); err != nil {
		t.Fatalf("the first press did not say what the second does: %v\n%s", err, term.Snapshot())
	}
	select {
	case err := <-hook.exited:
		t.Fatalf("ASSERTION: the first press answered: %v, printed %q", err, hook.stdout.String())
	case <-time.After(time.Second):
	}
	saveFrame(t, term, "inbox-risky-armed")

	if err := term.SendKeys("1"); err != nil {
		t.Fatalf("second press: %v", err)
	}
	select {
	case err := <-hook.exited:
		if err != nil {
			t.Fatalf("the hook failed: %v", err)
		}
	case <-time.After(uiTimeout):
		t.Fatalf("the hook never returned after the second press\n%s", term.Snapshot())
	}
	if out := hook.stdout.String(); !strings.Contains(out, `"behavior":"allow"`) {
		t.Fatalf("the hook printed %q, want Claude Code's allow", out)
	}
	alive(t, term, "after allowing a risky approval")
}

// TestInboxKeepsAPlanPlanningWithAReason is the plan round trip with the real
// hook: an ExitPlanMode is held under Plans with its title and length, shown
// whole, and n with a reason makes the hook print a deny carrying the reason.
func TestInboxKeepsAPlanPlanningWithAReason(t *testing.T) {
	base, term := approvalsClient(t)
	hook := startHeldHook(t, base, `{"hook_event_name":"PermissionRequest","session_id":"e2e-plan","permission_mode":"plan","tool_name":"ExitPlanMode","tool_input":{"plan":"# Refactor the retry loop\n1. Move backoff into api/retry.go.\n2. Add table tests.","planFilePath":"/tmp/plan.md"}}`)

	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Plans 1", "Refactor the retry loop (3 lines)", "Move backoff into api/retry.go.", "3 keep planning")
	}, uiTimeout); err != nil {
		t.Fatalf("the Inbox never showed the plan: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "inbox-plan-held")
	time.Sleep(answerSettle)

	if err := term.SendKeys("n"); err != nil {
		t.Fatalf("n: %v", err)
	}
	if err := term.WaitForText("Reason: _", uiTimeout); err != nil {
		t.Fatalf("n did not open the reason line: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("split step 2"); err != nil {
		t.Fatalf("type the reason: %v", err)
	}
	if err := term.WaitForText("Reason: split step 2_", uiTimeout); err != nil {
		t.Fatalf("the reason was not typed: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("send the reason: %v", err)
	}
	select {
	case err := <-hook.exited:
		if err != nil {
			t.Fatalf("the hook failed: %v", err)
		}
	case <-time.After(uiTimeout):
		t.Fatalf("the hook never returned after the reason\n%s", term.Snapshot())
	}
	out := hook.stdout.String()
	if !strings.Contains(out, `"behavior":"deny"`) || !strings.Contains(out, `"message":"split step 2"`) {
		t.Fatalf("the hook printed %q, want a deny with the reason", out)
	}
	alive(t, term, "after keeping a plan planning")
}
