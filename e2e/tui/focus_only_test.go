package tuie2e

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// focusOnlyRow is one row of list-windows --json.
type focusOnlyRow struct {
	Name      string `json:"display_name"`
	Minimized bool   `json:"minimized"`
	Focused   bool   `json:"focused"`
}

// waitFocusOnly waits until every pane of session matches want, by name:
// true is minimized.
func waitFocusOnly(t *testing.T, term *tuitest.Terminal, base, session, focused string, want map[string]bool, what string) {
	t.Helper()
	var last string
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		out, err := tuiosCLI(t, base, "list-windows", "--json", "--session", session)
		last = out
		if err == nil {
			var res struct {
				Windows []focusOnlyRow `json:"windows"`
			}
			_ = json.Unmarshal([]byte(out), &res)
			ok := len(res.Windows) == len(want)
			for _, r := range res.Windows {
				if m, known := want[r.Name]; !known || m != r.Minimized || (r.Name == focused) != r.Focused {
					ok = false
				}
			}
			if ok {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: %s\n%s\n%s", what, last, term.Snapshot())
}

// TestFocusOnlyHidesTheOtherPanes is issue 1723. ctrl+b m f minimizes every
// other pane on the workspace and keeps the focused one. A second press
// brings back the panes it hid, and only those: a pane the person minimized
// before stays minimized. The focus stays on the pane it was on.
func TestFocusOnlyHidesTheOtherPanes(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "")
	if out, err := tuiosCLI(t, base, "new", "fo", "--detach"); err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach", "fo"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, tuitest.Alt(tuitest.Esc))
	if err := term.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("never in window mode: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)
	for i, name := range []string{"ONE", "TWO", "THREE", "FOUR"} {
		if name != "ONE" {
			newWindow(t, term)
			want := i + 1
			if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == want }, uiTimeout); err != nil {
				t.Fatalf("window %s never opened: %v\n%s", name, err, term.Snapshot())
			}
		}
		if out, err := tuiosCLI(t, base, "set-window", "-s", "fo", "--name", name); err != nil {
			t.Fatalf("name %s: %v\n%s", name, err, out)
		}
	}
	// FOUR is minimized before ctrl+b m f, and must stay minimized at the
	// end. It is minimized from the command line: a key here raced the new
	// windows, so on a slow run nothing was minimized and the setup failed.
	if out, err := tuiosCLI(t, base, "set-window", "-s", "fo", "-w", "FOUR", "--minimize"); err != nil {
		t.Fatalf("minimize FOUR: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "focus-window", "-s", "fo", "TWO"); err != nil {
		t.Fatalf("focus TWO: %v\n%s", err, out)
	}
	waitFocusOnly(t, term, base, "fo", "TWO", map[string]bool{"ONE": false, "TWO": false, "THREE": false, "FOUR": true}, "the setup")
	time.Sleep(300 * time.Millisecond)

	sendKeys(t, term, tuitest.Ctrl('b'), "m", "f")
	waitFocusOnly(t, term, base, "fo", "TWO", map[string]bool{"ONE": true, "TWO": false, "THREE": true, "FOUR": true},
		"ctrl+b m f hid the other panes and kept TWO focused")
	time.Sleep(500 * time.Millisecond)

	sendKeys(t, term, tuitest.Ctrl('b'), "m", "f")
	waitFocusOnly(t, term, base, "fo", "TWO", map[string]bool{"ONE": false, "TWO": false, "THREE": false, "FOUR": true},
		"the second press brought back what it hid, FOUR stays minimized, TWO keeps the focus")
}
