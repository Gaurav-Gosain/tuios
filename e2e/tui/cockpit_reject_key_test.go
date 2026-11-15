package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// TestCockpitRRepliesAndXRejects holds a Bash call and reads its card in the
// agents view. r is reply on every row: on the held approval it rejects
// nothing, the hook stays held, and the view says what to press. X rejects, and the card and the key bar
// say so: "X  Reject" and "X reject".
//
// Negative control: on main r rejects the call at once, and X does nothing.
func TestCockpitRRepliesAndXRejects(t *testing.T) {
	base, term := approvalsClient(t)
	hook := startHeldHook(t, base, `{"hook_event_name":"PermissionRequest","session_id":"e2e-rx","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Allow Bash?", "$ go test")
	}, uiTimeout); err != nil {
		t.Fatalf("the agents view never showed the approval: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	dir := artifactDir(t)
	saveFrame(t, term, "r-x-card")
	savePNG(t, term.Screen(), shot.XTermPalette(), dir, "card")
	text := term.Screen().Text()
	if !strings.Contains(text, "X  Reject") || !strings.Contains(text, "X reject") || strings.Contains(text, "r reject") || strings.Contains(text, "r  Reject") {
		t.Errorf("ASSERTION: the card and the key bar do not name X as reject:\n%s", term.Snapshot())
	}
	// The dock shows the newest notice. The agent's own notice comes first,
	// so the words r shows are not hidden under it.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "needs approval")
	}, uiTimeout); err != nil {
		t.Fatalf("the dock never showed the approval notice: %v\n%s", err, term.Snapshot())
	}
	pause(t, "the view takes no answer key in the moment after a card is shown", answerSettle)

	// r rejects nothing: the call stays held.
	if err := term.SendKeys("r"); err != nil {
		t.Fatal(err)
	}
	stayTrue(t, "r leaves the held call held", 1500*time.Millisecond, func() (bool, string) {
		select {
		case err := <-hook.exited:
			hook.exited <- err
			return false, "the hook returned: " + hook.stdout.String() + "\n" + term.Snapshot()
		default:
		}
		// main opens the reject confirm on r.
		if screenHas(term.Screen(), "Press enter to reject") {
			return false, "r opened the reject confirm:\n" + term.Snapshot()
		}
		return true, ""
	})
	saveFrame(t, term, "r-x-after-r")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Allow Bash?", "$ go test", "X to reject")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: r did not say what to press on the held call: %v\n%s", err, term.Snapshot())
	}
	savePNG(t, term.Screen(), shot.XTermPalette(), dir, "after-r")
	pause(t, "the view takes no answer key in the moment after a card is shown", answerSettle)

	// X rejects.
	if err := term.SendKeys("X", tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-hook.exited:
		if err != nil {
			t.Fatalf("the hook failed: %v", err)
		}
	case <-time.After(uiTimeout):
		t.Fatalf("ASSERTION: X did not reject the call\n%s", term.Snapshot())
	}
	if out := hook.stdout.String(); !strings.Contains(out, `"behavior":"deny"`) {
		t.Fatalf("ASSERTION: X answered %q, want Claude Code's deny", out)
	}
	saveFrame(t, term, "r-x-after-x")
	alive(t, term, "after r and X on a held approval")
}
