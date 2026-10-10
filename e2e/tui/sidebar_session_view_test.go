package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestSidebarSessionViewsAreIndependent proves that each rail draws the live
// active pane of a different daemon session, while the center stays attached
// to its own session. A rail must not just list the other sessions' names.
func TestSidebarSessionViewsAreIndependent(t *testing.T) {
	const cfg = `[appearance.sidebar]
position = "right"
enabled = true
width = 38
sections = "sessions"
[appearance.sidebar.right]
session = "side-right"
[appearance.sidebar.left]
enabled = true
width = 38
session = "side-left"
`
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, cfg)
	for _, name := range []string{"center", "side-left", "side-right", "side-left-alt"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("new %s: %v\n%s", name, err, out)
		}
	}
	for _, item := range []struct{ session, marker, command string }{
		{"center", "CENTER-SESSION-VIEW", "printf '%s\\n' CENTER-SESSION-VIEW"},
		{"side-left", "LEFT-SESSION-VIEW", "printf '%s\\n' LEFT-SESSION-VIEW"},
		{"side-right", "RIGHT-SESSION-VIEW", "printf '%s\\n' RIGHT-SESSION-VIEW"},
		{"side-left-alt", "ALTERNATE-LEFT-VIEW", "printf '%s\\n' ALTERNATE-LEFT-VIEW"},
	} {
		if out, err := tuiosCLI(t, base, "send-keys", "-s", item.session, "--literal", item.command); err != nil {
			t.Fatalf("send to %s: %v\n%s", item.session, err, out)
		}
		if out, err := tuiosCLI(t, base, "send-keys", "-s", item.session, "Enter"); err != nil {
			t.Fatalf("enter in %s: %v\n%s", item.session, err, out)
		}
	}
	term := startIn(t, base, startOpts{cols: 138, rows: 40, args: []string{"attach", "center"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		cols, rows := s.Size()
		var left, center, right bool
		for y := 0; y < rows; y++ {
			line := []rune(s.Line(y))
			if len(line) < cols {
				line = append(line, []rune(strings.Repeat(" ", cols-len(line)))...)
			}
			left = left || strings.Contains(string(line[:38]), "LEFT-SESSION-VIEW")
			center = center || strings.Contains(string(line[38:cols-38]), "CENTER-SESSION-VIEW")
			right = right || strings.Contains(string(line[cols-38:]), "RIGHT-SESSION-VIEW")
		}
		return left && center && right
	}, uiTimeout); err != nil {
		t.Fatalf("three session viewports did not render independently: %v\n%s", err, term.Snapshot())
	}
	// A click grants the left session's pane the keyboard without changing the
	// center attachment. The marker is absent from the typed command, so the
	// assertion needs a real shell response and not terminal echo.
	mousePress(t, term, 15, 5, tuitest.MouseLeft, 0)
	mouseRelease(t, term, 15, 5, tuitest.MouseLeft, 0)
	runInShell(t, term, `printf '%s\n' LEFT""INPUT`, "LEFTINPUT", uiTimeout)
	if out, err := tuiosCLI(t, base, "capture-pane", "-s", "center"); err != nil || strings.Contains(out, "LEFTINPUT") {
		t.Fatalf("left rail input reached the center session: %v\n%s", err, out)
	}
	t.Logf("left, center, and right session panes with routed input:\n%s", term.Snapshot())

	// A normal new-pane shortcut while the left rail has the keyboard must
	// create a pane in its assigned session, not in the center session.
	if err := term.SendKeys(tuitest.Ctrl('b'), "c"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		left, leftErr := daemonWindows(base, "side-left")
		center, centerErr := daemonWindows(base, "center")
		if leftErr == nil && centerErr == nil && left.Total == 2 && center.Total == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("new-pane shortcut targeted the wrong session: left=%+v (%v), center=%+v (%v)\n%s", left, leftErr, center, centerErr, term.Snapshot())
		}
		time.Sleep(50 * time.Millisecond)
	}

	// A normal additional pane joins the left rail's vertical stack, rather
	// than appearing in the center or replacing the other visible panes.
	if out, err := tuiosCLI(t, base, "new-window", "next", "-s", "side-left"); err != nil {
		t.Fatalf("open second pane in left session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "side-left", "-w", "next", "--literal", `printf '%s\n' NEXT-ACTIVE-PANE`); err != nil {
		t.Fatalf("write to next active pane: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "side-left", "-w", "next", "Enter"); err != nil {
		t.Fatalf("enter in next active pane: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "NEXT-ACTIVE-PANE") && strings.Contains(s.Text(), "LEFT-SESSION-VIEW") &&
			strings.Contains(s.Line(0), "side-left  3/3")
	}, uiTimeout); err != nil {
		t.Fatalf("left rail did not stack the additional pane: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "p"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Line(0), "side-left  2/3") && strings.Contains(s.Text(), "NEXT-ACTIVE-PANE")
	}, uiTimeout); err != nil {
		t.Fatalf("previous-pane shortcut did not focus a stacked pane: %v\n%s", err, term.Snapshot())
	}
	previous, err := daemonWindows(base, "side-left")
	if err != nil || previous.FocusedWindowID == "" {
		t.Fatalf("read previous rail pane: %+v %v", previous, err)
	}
	if err := term.SendKeys("printf '%s\\n' PREVFOCUS", tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(uiTimeout)
	for {
		out, err := tuiosCLI(t, base, "capture-pane", "-s", "side-left", "-w", previous.FocusedWindowID)
		if err == nil && strings.Contains(out, "PREVFOCUS") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("previous-pane shortcut focused header but sent input elsewhere: %v\n%s\n%s", err, out, term.Snapshot())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if out, err := tuiosCLI(t, base, "capture-pane", "-s", "center"); err != nil || strings.Contains(out, "PREVFOCUS") {
		t.Fatalf("rail pane shortcut changed center session: %v\n%s", err, out)
	}
	// The rail header is a pane selector, not an ordinary session-list row.
	mousePress(t, term, 15, 0, tuitest.MouseLeft, 0)
	mouseRelease(t, term, 15, 0, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Line(0), "side-left  3/3") && strings.Contains(s.Text(), "NEXT-ACTIVE-PANE")
	}, uiTimeout); err != nil {
		t.Fatalf("rail header did not focus the next stacked pane: %v\n%s", err, term.Snapshot())
	}

	// The session assignments belong to distinct left/right Settings rows; a
	// shell-section editor cannot manage them and must not appear here.
	mousePress(t, term, 116, 12, tuitest.MouseRight, 0)
	mouseRelease(t, term, 116, 12, tuitest.MouseRight, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		content := s.Text()
		return strings.Contains(content, "Left session") && strings.Contains(content, "Right session") &&
			!strings.Contains(content, "Shell sections")
	}, uiTimeout); err != nil {
		t.Fatalf("Settings did not offer separate left/right session controls: %v\n%s", err, term.Snapshot())
	}
	t.Logf("session-first sidebar settings:\n%s", term.Snapshot())
	// Changing only the left assignment in Settings must replace its live view
	// without touching the right rail or the center attachment.
	if err := term.SendKeys(tuitest.Down, tuitest.Down, tuitest.Enter, tuitest.Ctrl('u'), "side-left-alt", tuitest.Enter); err != nil {
		t.Fatalf("edit left session assignment: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "Left session") && strings.Contains(s.Text(), "side-left-alt")
	}, uiTimeout); err != nil {
		t.Fatalf("Settings did not save the left assignment: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "ALTERNATE-LEFT-VIEW") && strings.Contains(text, "RIGHT-SESSION-VIEW") &&
			!strings.Contains(text, "LEFT-SESSION-VIEW")
	}, uiTimeout); err != nil {
		t.Fatalf("left reassignment did not change only the left view: %v\n%s", err, term.Snapshot())
	}
	t.Logf("Settings reassigned just the left rail:\n%s", term.Snapshot())
}
