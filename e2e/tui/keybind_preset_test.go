package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestTmuxPresetDrivesWorkspaces is the tmux preset on a real daemon: with
// keybindings.preset = "tmux", ctrl+b c opens a workspace, n and p step
// through them, a digit goes to one, and l goes back to the last. A p typed
// right after ctrl+b p reaches the shell instead of switching again, because
// the preset does not repeat p. ctrl+b ctrl+t m opens the minimize menu the
// preset moved m out of.
//
// Negative control: the same config on a build without presets. ctrl+b c
// makes a window on workspace 1, and the first wait for workspace 2 fails.
func TestTmuxPresetDrivesWorkspaces(t *testing.T) {
	term, base := panesSession(t, "tmuxkeys", "\n[keybindings]\npreset = \"tmux\"\n")
	waitForSettledGeometryIn(t, base, "tmuxkeys", 1)

	press := func(keys ...any) {
		t.Helper()
		if err := term.SendKeys(append([]any{tuitest.Ctrl('b')}, keys...)...); err != nil {
			t.Fatalf("press ctrl+b %v: %v", keys, err)
		}
	}
	// p and n step through workspaces that hold a pane, so each new
	// workspace's pane is waited for before the next key.
	paneOn := func(ws int) {
		t.Helper()
		deadline := time.Now().Add(uiTimeout)
		for time.Now().Before(deadline) {
			for _, p := range listPanes(t, base, "tmuxkeys") {
				if p.Workspace == ws {
					return
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("no pane opened on workspace %d", ws)
	}
	press("c")
	waitWorkspace(t, base, "tmuxkeys", 2)
	paneOn(2)
	press("c")
	waitWorkspace(t, base, "tmuxkeys", 3)
	paneOn(3)
	press("p")
	waitWorkspace(t, base, "tmuxkeys", 2)
	press("n")
	waitWorkspace(t, base, "tmuxkeys", 3)
	press("1")
	waitWorkspace(t, base, "tmuxkeys", 1)
	press("l")
	waitWorkspace(t, base, "tmuxkeys", 3)

	// ctrl+b p then p at once: one switch, and the second p is typed.
	enterTerminalMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "p", "p"); err != nil {
		t.Fatalf("press ctrl+b p p: %v", err)
	}
	waitWorkspace(t, base, "tmuxkeys", 2)
	time.Sleep(700 * time.Millisecond)
	if info := sessionInfoOf(t, base, "tmuxkeys"); info.CurrentWorkspace != 2 {
		t.Errorf("ctrl+b p p switched twice: on workspace %d, want 2", info.CurrentWorkspace)
	}

	press(tuitest.Ctrl('t'), "m")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "Minimize")
	}, uiTimeout); err != nil {
		t.Fatalf("ctrl+b ctrl+t m did not open the minimize menu: %v\n%s", err, term.Snapshot())
	}
}
