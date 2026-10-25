package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The Inbox draws a tool call from one page of a conversation, and its result
// can arrive on a later page. The thread keeps the cards it drew, so the result
// must reach the card that is still running: without that, a command that failed
// stays "running" in the person's view. Pi is the harness that drives it here,
// because a selection report moves the thread to the page with the result.
func TestCockpitThreadFoldsALateToolResult(t *testing.T) {
	r := newATRig(t, "pi-live-tool-result.txt", 1)
	path := piFixture(t, r)
	piHook(t, r, map[string]any{"hook_event_name": "session_start", "session_id": "pi-fixture", "transcript_path": path, "transcript_leaf": "a1", "busy": true})
	term := startIn(t, r.base, startOpts{args: []string{"attach", transcriptSession}, cols: 160, rows: 50, shippedLooks: true})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatal(err)
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "npm test") }, uiTimeout); err != nil {
		t.Fatal(err)
	}
	piUpdate(t, r, path, "pi-fixture", "r2")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "Command failed with exit code 1") && strings.Contains(s.Text(), "failed")
	}, uiTimeout); err != nil {
		t.Fatalf("incremental failure left the retained tool running: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "pi-live-tool-result")
}
