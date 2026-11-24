package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestAgentsViewJumpWhileAttachedThroughAHost goes to an agent's pane from the
// agents view of a client attached to a session on a linked machine, alpha
// (#2444). The agent runs in the attached session, in a pane that is not
// focused, so the jump has to move the focus there.
//
// The ways it can go wrong, each an assertion: the jump reads the row's empty
// host as this machine and tries to attach "far" here, which fails with
// "could not attach"; the jump closes the agents view and lands nowhere, so
// the other pane keeps the focus; the client leaves alpha.
func TestAgentsViewJumpWhileAttachedThroughAHost(t *testing.T) {
	f := startGlobalFleet(t)
	for _, args := range [][]string{
		{"set-window", "-s", "far", "-w", "0", "--name", "far-fix"},
		{"set-agent-state", "working", "-s", "far", "-w", "far-fix", "--harness", "claude-code"},
		{"new-window", "-s", "far", "far-shell"},
	} {
		if out, err := cliWithin(t, f.alpha, f.env, args...); err != nil {
			t.Fatalf("%v on alpha: %v\n%s", args, err, out)
		}
	}
	agent := alphaWindowID(t, f, "far-fix")
	term := startIn(t, f.here, startOpts{args: []string{"attach", "--host", "alpha", "far"}, cols: 140, rows: 44,
		env: append([]string{"TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0"}, f.env...)})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 2 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached through alpha: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if out, err := cliWithin(t, f.alpha, f.env, "focus-window", "-s", "far", "far-shell"); err != nil {
		t.Fatalf("focus far-shell on alpha: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(_ tuitest.Screen) bool { return alphaFocused(t, f) != agent }, uiTimeout); err != nil {
		t.Fatalf("the agent's pane kept the focus, so the jump has nothing to move: %v", err)
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	waitText(t, term, "the agent on alpha", "far-fix")
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "attached-jump-before")

	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("press enter on the agent: %v", err)
	}
	if err := term.WaitFor(func(_ tuitest.Screen) bool { return alphaFocused(t, f) == agent }, uiTimeout); err != nil {
		saveCockpitFrames(t, term, "attached-jump-missed")
		t.Fatalf("ASSERTION: enter on the agent did not focus its pane on alpha: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "attached-jump-landed")
	if text := term.Screen().Text(); strings.Contains(text, "could not attach") || strings.Contains(text, "on this machine") {
		t.Fatalf("ASSERTION: the jump tried to attach the session on this machine:\n%s", term.Snapshot())
	}
	if text := term.Screen().Text(); strings.Contains(text, "Search agents") {
		t.Fatalf("ASSERTION: the agents view is still open after the jump:\n%s", term.Snapshot())
	}
	if out, _ := cliWithin(t, f.alpha, f.env, "list-clients", "--json"); !strings.Contains(out, "far") {
		t.Fatalf("ASSERTION: the client left alpha's session far:\n%s", out)
	}
	alive(t, term, "after the jump on alpha")
}

// alphaWindowID is the id of the window named name in alpha's session far.
func alphaWindowID(t *testing.T, f *globalFleet, name string) string {
	t.Helper()
	out, err := cliWithin(t, f.alpha, f.env, "list-windows", "-s", "far", "--json")
	if err != nil {
		t.Fatalf("list-windows on alpha: %v\n%s", err, out)
	}
	var list struct {
		Windows []struct {
			ID      string `json:"window_id"`
			Display string `json:"display_name"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("list-windows --json on alpha printed %q: %v", out, err)
	}
	for _, w := range list.Windows {
		if w.Display == name {
			return w.ID
		}
	}
	t.Fatalf("alpha's session far has no window %q:\n%s", name, out)
	return ""
}

// alphaFocused is the focused window of alpha's session far, "" when it cannot
// be read.
func alphaFocused(t *testing.T, f *globalFleet) string {
	t.Helper()
	out, err := cliWithin(t, f.alpha, f.env, "list-windows", "-s", "far", "--json")
	if err != nil {
		return ""
	}
	var list struct {
		Focused string `json:"focused_window_id"`
	}
	if json.Unmarshal([]byte(out), &list) != nil {
		return ""
	}
	return list.Focused
}
