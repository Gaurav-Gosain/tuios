package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestApprovalHoldOutlastsACommandFocusMove: a held approval ends as viewed
// when the person turns to the pane, because the harness's own prompt is the
// quickest thing to answer there. A focus move made by a command is not the
// person turning to it. Every pane may run run-command by default, so before
// this an agent's `tuios run-command NextWindow` ended the person's hold and
// took the prompt out of the Inbox (M11 in the state machine audit).
//
// How this could pass wrongly, written down first:
//   - The hold could never have been held. The hook must still run three
//     seconds after it starts, before any focus move.
//   - The command could fail to move the focus at all. The test reads the
//     daemon's focused window after it, and it must be the held pane.
//   - A fix that never ends a hold on focus passes the first half. The person
//     then presses tab, which focuses the same pane, and the hold must end.
//   - focus-window, which the daemon carries out itself, never ended the hold;
//     it is used only to put the focus back between the two halves.
func TestApprovalHoldOutlastsACommandFocusMove(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	dir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[agents.approvals]\nenabled = [\"claude-code\"]\nhold_seconds = 60\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	cli := func(args ...string) string {
		t.Helper()
		out, err := tuiosCLI(t, base, args...)
		if err != nil {
			t.Fatalf("tuios %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	cli("new", "e2e-agent", "--detach")
	held := focusedWindowID(t, base, "e2e-agent")
	cli("new-window", "-s", "e2e-agent", "other")
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-agent"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 2 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("the client never settled in window management mode: %v\n%s", err, term.Snapshot())
	}
	focusOn := func(want, why string) {
		t.Helper()
		deadline := time.Now().Add(uiTimeout)
		for focusedWindowID(t, base, "e2e-agent") != want {
			if time.Now().After(deadline) {
				t.Fatalf("%s: the focus is on %s, want %s", why, focusedWindowID(t, base, "e2e-agent"), want)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	cli("focus-window", "-s", "e2e-agent", "other")
	other := windowIDByName(t, base, "e2e-agent", "other")
	focusOn(other, "focus-window other")
	time.Sleep(insertGuard)

	hook := startHeldHook(t, base, `{"hook_event_name":"PermissionRequest","session_id":"e2e-cmd-focus","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`)
	select {
	case err := <-hook.exited:
		t.Fatalf("the hook did not hold: %v, printed %q", err, hook.stdout.String())
	case <-time.After(3 * time.Second):
	}
	fmt.Fprintf(&log, "hold on %s, the person's client on %s\n", held, other)

	// An agent's command moves the focus to the held pane.
	cli("run-command", "-s", "e2e-agent", "NextWindow")
	focusOn(held, "run-command NextWindow")
	select {
	case err := <-hook.exited:
		fmt.Fprintf(&log, "run-command NextWindow: the hold ENDED\n")
		t.Fatalf("ASSERTION: run-command NextWindow ended the person's hold (hook exited %v, printed %q)", err, hook.stdout.String())
	case <-time.After(4 * time.Second):
		fmt.Fprintf(&log, "run-command NextWindow: focus on %s, the hold runs on\n", held)
	}

	// The person turns to the pane with tab: the hold ends there.
	cli("focus-window", "-s", "e2e-agent", "other")
	focusOn(other, "focus-window other again")
	time.Sleep(insertGuard)
	if err := term.SendKeys(tuitest.Tab); err != nil {
		t.Fatal(err)
	}
	focusOn(held, "tab")
	select {
	case <-hook.exited:
		fmt.Fprintf(&log, "tab from the person: the hold ended, the hook printed %q\n", hook.stdout.String())
	case <-time.After(uiTimeout):
		t.Fatalf("ASSERTION: the person's tab to the held pane did not end the hold")
	}

	path := filepath.Join(artifactDir(t), "approval-command-focus.txt")
	if err := os.WriteFile(path, []byte(log.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("steps:\n%s(saved to %s)", log.String(), path)
}
