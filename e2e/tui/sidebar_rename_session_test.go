package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestRailRightClickRenamesTheSessionUnderThePointer drives the menu's rename
// row against a real daemon, on a session this client is not attached to.
//
// It is the same shape as the kill test and guards the same class of defect: a
// row that names one session and acts on another. A rename that reached the
// attached session would be quieter than a kill and just as wrong, and a unit
// test on the menu builder cannot see it, because the substitution used to
// happen in the handler behind the row rather than in the row.
func TestRailRightClickRenamesTheSessionUnderThePointer(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	for _, name := range []string{"alpha", "bravo"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v: %s", name, err, out)
		}
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "alpha"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("normalise to window mode: %v", err)
	}
	if err := term.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("client never settled in window management mode: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)

	toggleSidebarViaPalette(t, term)
	waitForAll(t, term, uiTimeout, "sidebar with both session rows", sidebarHeader, "bravo")

	// Right-click the row of the session this client is NOT in.
	col, row := findOnScreen(t, term, "bravo")
	mouseClick(t, term, col, row, tuitest.MouseRight, 0)
	if err := term.WaitForText("Rename", uiTimeout); err != nil {
		t.Fatalf("the row's menu offered no rename: %v\n%s", err, term.Snapshot())
	}

	col, row = findOnScreen(t, term, "Rename")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	// The editor names the session it is about, which is the assertion that
	// fails first if the row handed the handler the attached session.
	if err := term.WaitForText("rename session bravo", uiTimeout); err != nil {
		t.Fatalf("the editor did not name the row's session: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys("Payments API"); err != nil {
		t.Fatalf("type the new label: %v", err)
	}
	if err := term.WaitForText("Payments API", uiTimeout); err != nil {
		t.Fatalf("the editor did not take the typed label: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("commit the rename: %v", err)
	}

	// The rail is the screen the user is looking at, so it is where the rename
	// has to land: bravo's row now reads by its new label and alpha's does not.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "Payments API") && strings.Contains(text, "alpha")
	}, uiTimeout); err != nil {
		t.Fatalf("the rail did not show the renamed session beside the attached one: %v\n%s", err, term.Snapshot())
	}

	// The daemon is the authority on which session was renamed, and these are
	// the assertions that would fail if the rename had reached the attached one.
	// session-info rather than ls: the label is display state, and ls lists the
	// names sessions are addressed by, which a rename deliberately leaves alone.
	out, err := tuiosCLI(t, base, "session-info", "--session", "bravo")
	if err != nil {
		t.Fatalf("session-info bravo: %v: %s", err, out)
	}
	if !strings.Contains(out, "Payments API") {
		t.Errorf("the daemon never took bravo's rename:\n%s", out)
	}
	out, err = tuiosCLI(t, base, "session-info", "--session", "alpha")
	if err != nil {
		t.Fatalf("session-info alpha: %v: %s", err, out)
	}
	if strings.Contains(out, "Payments API") {
		t.Errorf("the rename landed on the attached session:\n%s", out)
	}

	alive(t, term, "after renaming another session from the rail")
}
