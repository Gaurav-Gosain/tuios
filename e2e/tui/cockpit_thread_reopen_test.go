package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestCockpitThreadCatchesUpOnReopen checks that messages written while the
// agents view is closed appear on return, without another transcript append.
func TestCockpitThreadCatchesUpOnReopen(t *testing.T) {
	r := newATRig(t, "thread-reopen.txt", 1)
	path := filepath.Join(atProjects(r.base), "demo", "reopen.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(atRecord("first", "Before closing the view")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.join(r.wins[0], path, "reopen")
	term := startIn(t, r.base, startOpts{args: []string{"attach", transcriptSession}, cols: 160, rows: 50, shippedLooks: true})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatal(err)
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "Before closing the view") }, uiTimeout); err != nil {
		t.Fatalf("initial thread: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("\x1b"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1 && !strings.Contains(s.Text(), "Before closing the view")
	}, uiTimeout); err != nil {
		t.Fatalf("view did not close: %v\n%s", err, term.Snapshot())
	}
	sub := r.link.open(t, true)
	if _, err := sub.call(t, "subscribe", map[string]any{"session": transcriptSession, "types": []string{"transcript"}}); err != nil {
		t.Fatal(err)
	}
	appendLines(t, path, atRecord("second", "Written while the view was closed"))
	atEvent(t, sub, r.art, r.wins[0])
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "Before closing the view", "Written while the view was closed")
	}, uiTimeout); err != nil {
		t.Fatalf("reopened thread did not catch up: %v\n%s", err, term.Snapshot())
	}
}
