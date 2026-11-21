package tuie2e

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

func cockpitHeldClient(t *testing.T, payload, ready string) (string, *tuitest.Terminal) {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	writeConfig(t, base, "[agents.approvals]\nenabled = [\"claude-code\"]\nhold_seconds = 300\n")
	for _, s := range []string{"home", "api"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	startHookIn(t, base, "api", "0", payload)
	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 160, rows: 45, shippedLooks: true})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return screenHas(s, "1 needs you", ready) }, uiTimeout); err != nil {
		t.Fatalf("the card never showed %q: %v\n%s", ready, err, term.Snapshot())
	}
	return base, term
}

// TestCockpitAnswerKeysNeverFailInSilence presses a on a held approval the
// moment its card is drawn, before it has been on screen long enough to be
// read. The card says the key did not answer and why. A second a, once the
// card has been read, asks as it should, and the note goes. Then enter, in a
// client attached to another session, hands the approval back to its pane:
// the hold ends, and the client lands on the pane.
//
// Negative controls, recorded in NEGATIVE_CONTROLS.md: with the refusal shown
// only as a toast, the wait for the note on the card fails; with the jump
// sent before the hand back, the daemon refuses the hand back as not_human
// (the attach to api gave the client a new nonce) and the hold stays.
func TestCockpitAnswerKeysNeverFailInSilence(t *testing.T) {
	payload := `{"hook_event_name":"PermissionRequest","session_id":"e2e-silent","tool_name":"Bash","tool_input":{"command":"make test-a"}}`
	base, term := cockpitHeldClient(t, payload, "Allow once")
	if err := term.SendKeys("a"); err != nil {
		t.Fatalf("a: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Not answered", "press a again")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a pressed before the card was read did nothing the card shows: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "cockpit-answer-too-soon")
	if n := heldApprovals(t, base, "api"); n != 1 {
		t.Fatalf("ASSERTION: a pressed too soon answered the approval: %d held", n)
	}

	pause(t, "a held approval takes an answer key only after it has been on screen for a moment", answerSettle)
	if err := term.SendKeys("a"); err != nil {
		t.Fatalf("a again: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Press enter to allow") && !strings.Contains(s.Text(), "Not answered")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a second a did not ask, or the note stayed: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("esc: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Press enter to allow") }, uiTimeout); err != nil {
		t.Fatalf("esc did not drop the ask: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("enter: %v", err)
	}
	waitUntil(t, "the hold ends when enter hands the approval back to its pane", uiTimeout, func() (bool, string) {
		n := heldApprovals(t, base, "api")
		return n == 0, strconv.Itoa(n) + " held"
	})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "needs you") }, uiTimeout); err != nil {
		t.Fatalf("the agents view did not close on enter: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "cockpit-handed-back")
	alive(t, term, "after an approval was handed back to its pane")
}

// TestCockpitPlanCardSaysWhyItIsNotApproved holds a plan longer than its
// card. a before the end has been shown says on the card why it did not
// approve and how much is left.
//
// Negative control, recorded in NEGATIVE_CONTROLS.md: on main the refusal is
// a toast only.
func TestCockpitPlanCardSaysWhyItIsNotApproved(t *testing.T) {
	var plan strings.Builder
	plan.WriteString("# Rework the retry loop\\n")
	for i := 1; i <= 60; i++ {
		fmt.Fprintf(&plan, "%d. Step %d of the rework.\\n", i, i)
	}
	payload := `{"hook_event_name":"PermissionRequest","session_id":"e2e-plan-key","permission_mode":"plan","tool_name":"ExitPlanMode","tool_input":{"plan":"` + plan.String() + `","planFilePath":"/tmp/plan.md"}}`
	base, term := cockpitHeldClient(t, payload, "Read to the end to approve")
	pause(t, "a held plan takes an answer key only after it has been on screen for a moment", answerSettle)
	if err := term.SendKeys("a"); err != nil {
		t.Fatalf("a: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Not approved", "lines are left to read")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a on a plan not read to its end did not say why on the card: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "cockpit-plan-read-first")
	if n := heldApprovals(t, base, "api"); n != 1 {
		t.Fatalf("ASSERTION: a approved a plan not read to its end: %d held", n)
	}
	alive(t, term, "after a on an unread plan")
}
