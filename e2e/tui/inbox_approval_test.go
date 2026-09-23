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

// permissionRequest is the payload Claude Code sends its PermissionRequest
// hook for a Bash call.
const permissionRequest = `{"hook_event_name":"PermissionRequest","session_id":"e2e-approval","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`

// TestInboxAnswersAHeldApproval is the approval round trip against a real
// daemon, a real client and the real hook binary: with [agents.approvals]
// naming claude-code, the hook for a PermissionRequest in a session nobody is
// looking at reports the pane blocked and then waits. The Inbox shows the
// item with the keys that answer it, 1 allows it once, and the hook exits with
// Claude Code's allow decision on stdout. The item closes as answered.
//
// Negative control: without the [agents.approvals] table the hook prints
// nothing and exits at once, so the Inbox never shows answer keys and the
// first wait fails.
func TestInboxAnswersAHeldApproval(t *testing.T) {
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
	if out, err := tuiosCLI(t, base, "new", "e2e-home", "--detach"); err != nil {
		t.Fatalf("create the attached session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new", "e2e-agent", "--detach"); err != nil {
		t.Fatalf("create the agent's session: %v\n%s", err, out)
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

	// The hook, run the way Claude Code runs it, for the agent's pane.
	hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "e2e-agent", "--window", "0")
	hook.Dir = workDirIn(t, base)
	hook.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
	}
	hook.Stdin = strings.NewReader(permissionRequest)
	var stdout bytes.Buffer
	hook.Stdout = &stdout
	if err := hook.Start(); err != nil {
		t.Fatalf("start the hook: %v", err)
	}
	exited := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		exited <- hook.Wait()
		close(finished)
	}()
	t.Cleanup(func() {
		_ = hook.Process.Kill()
		<-finished
	})

	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	// The answer keys are said in text on the row, and again in the hints.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Approvals 1", "[1/3] approve Bash: go test", "allow", "deny", "answer in pane")
	}, uiTimeout); err != nil {
		t.Fatalf("the Inbox never offered to answer the held approval: %v\n%s", err, term.Snapshot())
	}
	select {
	case err := <-exited:
		t.Fatalf("the hook returned before anyone answered: %v, printed %q", err, stdout.String())
	default:
	}
	saveFrame(t, term, "inbox-approval-held")

	if err := term.SendKeys("1"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("the hook failed: %v", err)
		}
	case <-time.After(uiTimeout):
		t.Fatalf("the hook never returned after the answer\n%s", term.Snapshot())
	}
	out := stdout.String()
	if !strings.Contains(out, `"hookEventName":"PermissionRequest"`) || !strings.Contains(out, `"behavior":"allow"`) {
		t.Fatalf("the hook printed %q, want Claude Code's allow decision", out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Approvals 1")
	}, uiTimeout); err != nil {
		t.Fatalf("the answered approval stayed in the Inbox: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "inbox-approval-answered")

	// The daemon moved the pane on for the hook, so it is no longer blocked.
	state, _ := tuiosCLI(t, base, "get-agent-state", "-s", "e2e-agent", "-w", "0")
	if strings.Contains(state, "needs_input") {
		t.Errorf("the pane is still blocked after the answer:\n%s", state)
	}
	alive(t, term, "after answering an approval from the Inbox")
}
