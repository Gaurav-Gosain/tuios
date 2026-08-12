package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestRailRenamesAndAccentsAWindow drives the rail's own r and c keys against a
// real client: the rename editor appears on the row, commits to it, and the
// accent picker opens and takes a swatch. Both are rail-scope keys, so the proof
// that they work is what the rail draws afterwards.
func TestRailRenamesAndAccentsAWindow(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)

	if err := term.SendKeys("n"); err != nil {
		t.Fatalf("open a window: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "╰──")
	}, bootTimeout); err != nil {
		t.Fatalf("the window never appeared: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)

	toggleSidebarViaPalette(t, term)
	if err := term.WaitForText(sidebarHeader, uiTimeout); err != nil {
		t.Fatalf("sidebar did not open: %v\n%s", err, term.Snapshot())
	}

	// Focus the rail and step onto the window row under the session row.
	if err := term.SendKeys("s"); err != nil {
		t.Fatalf("focus the rail: %v", err)
	}
	if err := term.SendKeys("j"); err != nil {
		t.Fatalf("move the rail cursor: %v", err)
	}
	time.Sleep(insertGuard)

	if err := term.SendKeys("r"); err != nil {
		t.Fatalf("start the rename: %v", err)
	}
	if err := term.SendKeys("RAILNAME"); err != nil {
		t.Fatalf("type the new name: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return renameDialogUp(s) && strings.Contains(s.Text(), "RAILNAME")
	}, uiTimeout); err != nil {
		t.Fatalf("the rename dialog never carried the typed name: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("commit the rename: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "RAILNAME") && !renameDialogUp(s)
	}, uiTimeout); err != nil {
		t.Fatalf("the rename did not commit to the row: %v\n%s", err, term.Snapshot())
	}

	// The colour picker opens on the same row and takes a colour. The hex field
	// is the part a keyboard-driven test can steer without guessing at cells.
	if err := term.SendKeys("c"); err != nil {
		t.Fatalf("open the accent picker: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "accent") && strings.Contains(text, "hex")
	}, uiTimeout); err != nil {
		t.Fatalf("the accent picker did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("3b82f6"); err != nil {
		t.Fatalf("type a colour: %v", err)
	}
	if err := term.WaitForText("#3b82f6", uiTimeout); err != nil {
		t.Fatalf("the hex field did not take the typed colour: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("apply the colour: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return !strings.Contains(text, "hex") && strings.Contains(text, "RAILNAME")
	}, uiTimeout); err != nil {
		t.Fatalf("the picker did not close back onto the rail: %v\n%s", err, term.Snapshot())
	}

	alive(t, term, "after renaming and accenting from the rail")
}
