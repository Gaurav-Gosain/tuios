package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestCockpitReplyEchoOnAHost replies from this machine's agents view to an
// agent on a linked machine, alpha. The reply goes over the link and waits in
// alpha's queue as the link's. The thread shows it queued at once; a client
// opened later shows it again, read from alpha's queue with list-queued over
// the link; and cancel drops it there.
//
// The ways it can go wrong, each an assertion: a linked agent's thread shows
// no echo; a new client cannot find the reply, as it asked only this
// machine's queue; cancel drops nothing on alpha, as it ran here.
func TestCockpitReplyEchoOnAHost(t *testing.T) {
	f := startGlobalFleet(t)
	for _, args := range [][]string{
		{"set-window", "-s", "far", "-w", "0", "--name", "far-fix"},
		{"set-agent-state", "working", "-s", "far", "-w", "far-fix", "--harness", "claude-code"},
	} {
		if out, err := cliWithin(t, f.alpha, f.env, args...); err != nil {
			t.Fatalf("%v on alpha: %v\n%s", args, err, out)
		}
	}
	open := func() *tuitest.Terminal {
		term := startIn(t, f.here, startOpts{args: []string{"attach", "home"}, cols: 140, rows: 44,
			env: append([]string{"TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0"}, f.env...)})
		if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
			t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
		}
		windowManagementMode(t, term)
		if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
			t.Fatalf("open the agents view: %v", err)
		}
		waitText(t, term, "the far agent", "far-fix")
		_ = term.WaitStable(uiTimeout)
		return term
	}
	term := open()
	sendReply(t, term, "Check the far build.")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "Check the far build.", "Queued, typed when it rests", "cancel")
	}, uiTimeout); err != nil {
		saveCockpitFrames(t, term, "host-echo-missing")
		t.Fatalf("ASSERTION: the reply to the far agent does not show as queued: %v\n%s", err, term.Snapshot())
	}
	if ls, _ := cliWithin(t, f.alpha, f.env, "queue", "ls", "-s", "far"); !strings.Contains(ls, "Check the far build.") {
		t.Fatalf("the reply is not in alpha's queue:\n%s", ls)
	}
	saveCockpitFrames(t, term, "host-echo-queued")

	_ = term.Close()
	term = open()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "Check the far build.", "Queued, typed when it rests")
	}, uiTimeout); err != nil {
		saveCockpitFrames(t, term, "host-echo-reopen-missing")
		t.Fatalf("ASSERTION: a new client does not show the reply queued on alpha: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "host-echo-reopened")

	row, col := findText(t, term, "cancel")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Check the far build.") }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: cancel did not take the far reply away: %v\n%s", err, term.Snapshot())
	}
	if ls, _ := cliWithin(t, f.alpha, f.env, "queue", "ls", "-s", "far"); strings.Contains(ls, "Check the far build.") {
		t.Fatalf("ASSERTION: the cancelled reply is still in alpha's queue:\n%s", ls)
	}
	saveCockpitFrames(t, term, "host-echo-cancelled")
	alive(t, term, "after the far reply echo")
}
