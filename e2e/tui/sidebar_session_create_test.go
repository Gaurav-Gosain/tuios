package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// sidebarBand is how far right the sidebar column reaches. Session names that
// start within it are rail rows; the same name in a dock notification starts
// well past it, so the two cannot be confused.
const sidebarBand = 28

// sidebarHasRow reports whether the rail lists name as a row of its own, which
// is the only place the sidebar can be said to "show" a session.
func sidebarHasRow(s tuitest.Screen, name string) bool {
	head := screenRowOf(s, sidebarHeader)
	if head < 0 {
		return false
	}
	_, rows := s.Size()
	for r := head + 1; r < min(head+9, rows); r++ {
		if i := strings.Index(s.Line(r), name); i >= 0 && i < sidebarBand {
			return true
		}
	}
	return false
}

// TestSidebarKeepsSessionCreatedFromInside is the reported repro, end to end:
// with one session running, create a second from inside it, switch to it, then
// switch back. The new session used to vanish from the rail on the way back,
// because the client cache the rail builds foreign rows from never learned the
// session existed and the poll that would have found it is gated off while the
// cache believes there is only one session.
func TestSidebarKeepsSessionCreatedFromInside(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "origin", "--detach"); err != nil {
		t.Fatalf("create origin: %v: %s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "origin"}})
	// A restored pane's bottom border is the first proof the client is attached
	// and drawing the session it was given.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "╰──")
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
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return sidebarHasRow(s, "origin")
	}, uiTimeout); err != nil {
		t.Fatalf("rail never listed the attached session: %v\n%s", err, term.Snapshot())
	}

	// Create a session from inside this one: the switcher creates whatever name
	// does not match an existing session.
	if err := term.SendKeys(tuitest.Ctrl('b'), "S"); err != nil {
		t.Fatalf("open session switcher: %v", err)
	}
	if err := term.WaitForText("origin", uiTimeout); err != nil {
		t.Fatalf("session switcher never opened: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("spawned"); err != nil {
		t.Fatalf("type the new session name: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("create the session: %v", err)
	}
	if err := term.WaitForText("Session: spawned", uiTimeout); err != nil {
		t.Fatalf("never switched to the created session: %v\n%s", err, term.Snapshot())
	}

	// It is on the rail while attached, next to the one it was created from.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return sidebarHasRow(s, "spawned") && sidebarHasRow(s, "origin")
	}, uiTimeout); err != nil {
		t.Fatalf("rail did not list both sessions after the create: %v\n%s", err, term.Snapshot())
	}

	// Switch back to the original by clicking its rail row.
	col, row := findOnScreen(t, term, "origin")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitForText("Session: origin", uiTimeout); err != nil {
		t.Fatalf("clicking the origin row did not switch back: %v\n%s", err, term.Snapshot())
	}

	// The regression: the created session must still be on the rail.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return sidebarHasRow(s, "spawned")
	}, uiTimeout); err != nil {
		t.Fatalf("the created session vanished from the rail after switching back: %v\n%s", err, term.Snapshot())
	}

	alive(t, term, "after creating a session and switching back")
}
