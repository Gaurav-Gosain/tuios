package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestInboxAnswerOneStepAtATime walks the answer steps on one held risky call
// in the agents view and holds that each step replaces the one before, and
// that none of them answers on its own:
//
//   - 1 asks: the card says enter allows;
//   - R with the ask open only drops the ask: no reason line opens;
//   - R again opens the reason line, and a 1 typed into it is text, not an
//     answer;
//   - esc closes the line, and the call is still held;
//   - 1 and enter then allow it, so the steps still lead to an answer.
//
// How this could pass wrongly, written down first:
//   - A step could answer without the screen showing it. After each step the
//     test checks the held hook has not returned.
//
// This is a guard for the one answer step (inbox_answer.go): no run of keys
// leaves two steps standing on main either, so it has no failing control.
func TestInboxAnswerOneStepAtATime(t *testing.T) {
	base, term := approvalsClient(t)
	hook := startHeldHook(t, base, `{"hook_event_name":"PermissionRequest","session_id":"e2e-steps","tool_name":"Bash","tool_input":{"command":"rm -rf build/"}}`)
	sendKeys(t, term, tuitest.Ctrl('b'), "i")
	waitText(t, term, "the held risky call", "1 needs you", "risky", "R say why")
	time.Sleep(answerSettle)
	held := func(what string) {
		t.Helper()
		select {
		case err := <-hook.exited:
			t.Fatalf("ASSERTION: %s answered the call: %v, printed %q", what, err, hook.stdout.String())
		case <-time.After(700 * time.Millisecond):
		}
	}
	const ask, reason = "Press enter to allow", "Type the reason"
	has := func(s string) bool { return strings.Contains(term.Screen().Text(), s) }

	sendKeys(t, term, "1")
	waitText(t, term, "the ask", ask)
	held("1")

	sendKeys(t, term, "R")
	_ = term.WaitStable(2 * time.Second)
	if has(ask) || has(reason) {
		t.Errorf("ASSERTION: R with the ask open left the ask or opened the reason line\n%s", term.Snapshot())
	}
	held("R over the ask")

	sendKeys(t, term, "R")
	waitText(t, term, "the reason line", reason)
	sendKeys(t, term, "1")
	_ = term.WaitStable(2 * time.Second)
	if has(ask) {
		t.Errorf("ASSERTION: a 1 typed into the reason line asked to allow\n%s", term.Snapshot())
	}
	held("1 in the reason line")

	sendKeys(t, term, tuitest.Esc)
	_ = term.WaitStable(2 * time.Second)
	if has(reason) {
		t.Errorf("ASSERTION: esc did not close the reason line\n%s", term.Snapshot())
	}
	held("esc out of the reason line")

	sendKeys(t, term, "1")
	waitText(t, term, "the ask again", ask)
	sendKeys(t, term, tuitest.Enter)
	select {
	case err := <-hook.exited:
		if err != nil {
			t.Fatalf("the hook failed: %v", err)
		}
	case <-time.After(uiTimeout):
		t.Fatalf("ASSERTION: 1 and enter did not allow the call after the steps\n%s", term.Snapshot())
	}
	if out := hook.stdout.String(); !strings.Contains(out, `"behavior":"allow"`) {
		t.Fatalf("the hook printed %q, want Claude Code's allow", out)
	}
}
