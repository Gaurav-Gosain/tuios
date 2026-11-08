package tuie2e

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// paneShownRow is one row of list-windows --json, for the tests of a focus that
// has to show a minimized pane.
type paneShownRow struct {
	Name      string `json:"display_name"`
	Minimized bool   `json:"minimized"`
	Focused   bool   `json:"focused"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

// paneShown reports whether the window named name in session is restored,
// focused and has a size.
func paneShown(t *testing.T, base, session, name string) (bool, string) {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "--json", "--session", session)
	if err != nil {
		return false, out
	}
	var res struct {
		Windows []paneShownRow `json:"windows"`
	}
	_ = json.Unmarshal([]byte(out), &res)
	for _, r := range res.Windows {
		if r.Name == name {
			return !r.Minimized && r.Focused && r.Width > 0 && r.Height > 0, out
		}
	}
	return false, out
}

// waitPaneShown waits until shownPane holds.
func waitPaneShown(t *testing.T, term *tuitest.Terminal, base, session, name, what string) {
	t.Helper()
	var last string
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		ok, out := paneShown(t, base, session, name)
		if ok {
			return
		}
		last = out
		time.Sleep(100 * time.Millisecond)
	}
	snap := ""
	if term != nil {
		snap = term.Snapshot()
	}
	t.Fatalf("ASSERTION: %s: %s is not restored, focused and sized\n%s\n%s", what, name, last, snap)
}

// minimizeByKey focuses the pane name and minimizes it with m in window mode,
// the way a person does, so the client saves its box.
func minimizeByKey(t *testing.T, term *tuitest.Terminal, base, session, name string) {
	t.Helper()
	if out, err := tuiosCLI(t, base, "focus-window", "-s", session, name); err != nil {
		t.Fatalf("focus %s: %v\n%s", name, err, out)
	}
	time.Sleep(300 * time.Millisecond)
	sendKeys(t, term, "m")
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		out, _ := tuiosCLI(t, base, "list-windows", "--json", "--session", session)
		var res struct {
			Windows []paneShownRow `json:"windows"`
		}
		_ = json.Unmarshal([]byte(out), &res)
		for _, r := range res.Windows {
			if r.Name == name && r.Minimized {
				time.Sleep(500 * time.Millisecond) // the minimize animation
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("m never minimized %s\n%s", name, term.Snapshot())
}

// TestFocusRestoresAMinimizedPane is issue 1731. Selecting a minimized pane
// in the pane navigator, and tuios focus-window on one, focused it and left
// it minimized, so the selection looked like it did nothing. Both must
// restore it. A session with no client attached has no screen, and the
// daemon must still mark the pane restored.
func TestFocusRestoresAMinimizedPane(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "")
	if out, err := tuiosCLI(t, base, "new", "nav", "--detach"); err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "nav", "-w", "0", "--name", "AGENTONE"); err != nil {
		t.Fatalf("name: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach", "nav"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, tuitest.Alt(tuitest.Esc))
	if err := term.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("never in window mode: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)
	newWindow(t, term)
	if out, err := tuiosCLI(t, base, "set-window", "-s", "nav", "--name", "PLAINPANE"); err != nil {
		t.Fatalf("name: %v\n%s", err, out)
	}

	t.Run("navigator", func(t *testing.T) {
		minimizeByKey(t, term, base, "nav", "AGENTONE")
		sendKeys(t, term, tuitest.Ctrl('b'), "/")
		waitScreen(t, term, "the navigator never showed", "Panes")
		sendKeys(t, term, "/")
		if err := term.SendKeys("AGENTONE"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(500 * time.Millisecond)
		sendKeys(t, term, tuitest.Enter)
		waitPaneShown(t, term, base, "nav", "AGENTONE", "the navigator selection")
	})

	t.Run("focus-window", func(t *testing.T) {
		minimizeByKey(t, term, base, "nav", "PLAINPANE")
		if out, err := tuiosCLI(t, base, "focus-window", "-s", "nav", "PLAINPANE"); err != nil {
			t.Fatalf("focus-window: %v\n%s", err, out)
		}
		waitPaneShown(t, term, base, "nav", "PLAINPANE", "focus-window with a client attached")
	})

	t.Run("detached", func(t *testing.T) {
		for _, args := range [][]string{
			{"new", "far", "--detach"},
			{"set-window", "-s", "far", "-w", "0", "--name", "LONEPANE"},
			{"set-window", "-s", "far", "-w", "LONEPANE", "--minimize"},
			{"focus-window", "-s", "far", "LONEPANE"},
		} {
			if out, err := tuiosCLI(t, base, args...); err != nil {
				t.Fatalf("%v: %v\n%s", args, err, out)
			}
		}
		ok, out := paneShown(t, base, "far", "LONEPANE")
		if !ok {
			t.Fatalf("ASSERTION: focus-window with no client left LONEPANE minimized\n%s", out)
		}
	})
}
