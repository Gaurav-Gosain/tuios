package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestRailRightClickRenamesTheSessionUnderThePointer drives the menu's rename
// row against a real daemon, on a session this client is not attached to.
// The rename changes the session's name, so ls lists the new one.
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

	// The editor opens on the session's name. Clear it and type the new one.
	if err := term.SendKeys(strings.Repeat("\x7f", 8)); err != nil {
		t.Fatalf("clear the editor: %v", err)
	}
	if err := term.SendKeys("payments"); err != nil {
		t.Fatalf("type the new name: %v", err)
	}
	if err := term.WaitForText("payments", uiTimeout); err != nil {
		t.Fatalf("the editor did not take the typed name: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("commit the rename: %v", err)
	}

	// The rail is the screen the user is looking at, so it is where the rename
	// has to land: bravo's row now reads by its new name and alpha's does not.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "payments") && strings.Contains(text, "alpha")
	}, uiTimeout); err != nil {
		t.Fatalf("the rail did not show the renamed session beside the attached one: %v\n%s", err, term.Snapshot())
	}

	// The daemon is the authority on which session was renamed, and ls is
	// what a person checks (issue #266). These fail if the rename reached the
	// attached session or only set a label. The rail can show the new name a
	// moment before the daemon lists it, so ls is read until it does.
	names := sessionNames(t, base)
	for deadline := time.Now().Add(uiTimeout); !hasName(names, "payments") && time.Now().Before(deadline); {
		time.Sleep(100 * time.Millisecond)
		names = sessionNames(t, base)
	}
	if !hasName(names, "payments") || hasName(names, "bravo") {
		t.Errorf("tuios ls lists %v, want payments in place of bravo", names)
	}
	if !hasName(names, "alpha") {
		t.Errorf("the rename landed on the attached session: ls lists %v", names)
	}

	// Nerd Font icons in a name (discussion 1094). They are private use
	// area code points, and the rail and the dock used to drop them. A
	// name set from the CLI has to reach the screen with its icon, in one
	// cell, so the text after it does not move. U+E795 is the terminal
	// icon from the BMP block, U+F0219 a Material Design icon from plane 15.
	const sessIcon, wsIcon = "\ue795", "\U000f0219"
	if out, err := tuiosCLI(t, base, "rename-session", "payments", sessIcon+" payments"); err != nil {
		t.Fatalf("rename-session to a name with an icon: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-workspace-name", "-s", "alpha", "1", wsIcon+" home"); err != nil {
		t.Fatalf("set-workspace-name with an icon: %v\n%s", err, out)
	}
	// iconBefore reports whether icon is drawn in the cell right before
	// " "+word on some row, which is what one cell wide means on screen.
	iconBefore := func(s tuitest.Screen, icon, word string) bool {
		cols, rows := s.Size()
		for r := range rows {
			for x := 0; x+1+len(word) < cols; x++ {
				if s.Cell(x, r).Content != icon || s.Cell(x+1, r).Content != " " {
					continue
				}
				match := true
				for i := range len(word) {
					if s.Cell(x+2+i, r).Content != word[i:i+1] {
						match = false
						break
					}
				}
				if match {
					return true
				}
			}
		}
		return false
	}
	err := term.WaitFor(func(s tuitest.Screen) bool {
		return iconBefore(s, sessIcon, "payments") && iconBefore(s, wsIcon, "home")
	}, uiTimeout)
	saveArtifact(t, term, artifactDir(t), "nerd-font-names")
	savePNG(t, term.Screen(), hostPalette(t, ""), artifactDir(t), "nerd-font-names")
	if err != nil {
		t.Fatalf("the rail and the dock did not draw the names with their icons, one cell each: %v\n%s", err, term.Snapshot())
	}

	alive(t, term, "after renaming another session from the rail")

	// With Nerd Fonts off the icon would be a tofu box, so it is still
	// dropped. TERM=linux is the Linux console, which tuios knows has no
	// Nerd Font glyphs. The dock there still names the workspace.
	plain := startIn(t, base, startOpts{args: []string{"attach", "alpha"}, env: []string{"TERM=linux"}})
	err = plain.WaitFor(func(s tuitest.Screen) bool {
		if !strings.Contains(s.Text(), "home") {
			return false
		}
		cols, rows := s.Size()
		for r := range rows {
			for x := range cols {
				if c := s.Cell(x, r).Content; c == wsIcon || c == sessIcon {
					return false
				}
			}
		}
		return true
	}, uiTimeout)
	if err != nil {
		t.Fatalf("with Nerd Fonts off, the dock did not draw the workspace name without its icon: %v\n%s", err, plain.Snapshot())
	}
}
