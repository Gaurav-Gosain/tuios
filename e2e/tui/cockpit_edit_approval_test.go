package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestCockpitHeldEditIsReviewedInThePane holds a Claude Code Edit through the
// real hook. The TUI draws no diff, so the agents view shows the edit's card
// as "Review this edit in the pane" with Open the pane and Reject, and no
// allow. a and 1 answer nothing and say where to review it; the hold runs on.
// The frame is saved under artifactDir as cockpit-held-edit.
//
// Negative control, recorded in NEGATIVE_CONTROLS.md: with inboxIsEdit always
// false, the card offers Allow once and a allows the edit.
func TestCockpitHeldEditIsReviewedInThePane(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	writeConfig(t, base, "[agents.approvals]\nenabled = [\"claude-code\"]\nhold_seconds = 300\n")
	for _, s := range []string{"home", "api"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	payload := `{"hook_event_name":"PermissionRequest","session_id":"e2e-edit","tool_name":"Edit","tool_input":` +
		`{"file_path":"notes.txt","old_string":"retries = 3\n","new_string":"retries = 5\n"}}`
	startHookIn(t, base, "api", "0", payload)

	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 160, rows: 40, shippedLooks: true})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Review Edit", "Review this edit in the pane", "Open the pane", "Reject")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the edit's card does not send the person to the pane: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "cockpit-held-edit")
	if text := term.Screen().Text(); strings.Contains(text, "Allow once") || strings.Contains(text, "a allow") {
		t.Fatalf("ASSERTION: the edit's card offers an allow\n%s", term.Snapshot())
	}
	time.Sleep(answerSettle)
	for _, key := range []string{"a", "1"} {
		if err := term.SendKeys(key); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if err := term.WaitForText("Review this edit in the pane. Enter opens the pane.", uiTimeout); err != nil {
			t.Fatalf("ASSERTION: %s on a held edit did not say where to review it: %v\n%s", key, err, term.Snapshot())
		}
		time.Sleep(answerSettle)
		if n := heldApprovals(t, base, "api"); n != 1 {
			t.Fatalf("ASSERTION: %s answered a held edit: %d held", key, n)
		}
	}
	alive(t, term, "after allow keys on a held edit")
}
