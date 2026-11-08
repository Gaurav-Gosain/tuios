package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The agents view takes the keys while it is up, but the keys that open the
// palette, the launcher, help and settings act over it, the way they act over
// a pane. Each one opens on top of the view, takes the keys it is given, and
// closes back to the view as the person left it.

// ckCursorRow is the fleet row that wears the cursor bar, or "" when none
// does.
func ckCursorRow(s tuitest.Screen) string {
	_, rows := s.Size()
	for r := range rows {
		if col := fleetCol(s, r); strings.HasPrefix(strings.TrimLeft(col, " │"), "▌") {
			return strings.TrimSpace(col)
		}
	}
	return ""
}

// ckViewUp reports whether the agents view is drawn: its group headings and
// a row of the fleet are on screen.
func ckViewUp(s tuitest.Screen) bool {
	return containsAll(s.Text(), "Ready for review", "release-not")
}

// waitCkBack waits until the panel's title is gone and the view is drawn
// again with the cursor on the same row.
func waitCkBack(t *testing.T, term *tuitest.Terminal, title, cursor, when string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), title) && ckViewUp(s) && ckCursorRow(s) == cursor
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: %s: the view did not come back as it was (cursor %q, now %q): %v\n%s",
			when, cursor, ckCursorRow(term.Screen()), err, term.Snapshot())
	}
}

// TestCockpitGlobalKeysOverTheView opens the agents view on the fleet and
// then each panel from inside it. ctrl+p opens the palette over the view: the
// view stays drawn around it, j and k typed into the query do not move the
// list, and esc closes the palette alone. ? opens help over the view, which
// stays drawn behind it. The leader chord for settings opens settings over
// it. A click on the view outside the palette closes the palette and selects
// nothing. A palette row that goes to another workspace closes the view,
// which opens again on the same agent. With the reply editor open, ctrl+p
// opens the palette and the draft is kept; over the New agent sheet it opens
// too, and esc leaves the sheet as it was. In terminal mode the leader chord
// reaches the prefix table too, which the settings chord shows: the view
// binds no comma.
//
// Negative controls, recorded in NEGATIVE_CONTROLS.md: with handleInboxInput
// taking every key again the palette check fails; with ? closing the view
// before it opens help the help check fails; with the view taking the leader
// in terminal mode the terminal mode check fails; with a click on the view
// under a panel given to the view, the click check fails.
func TestCockpitGlobalKeysOverTheView(t *testing.T) {
	term, _, _, _ := startCockpitFleet(t, 200, 50, nil)
	cursorTo(t, term, func(line string) bool { return strings.Contains(line, "bench-idle") })
	cursor := ckCursorRow(term.Screen())
	saveCockpitFrames(t, term, "global-keys-0-view")

	// The palette, over the view.
	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("press ctrl+p: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), paletteTitle) && ckViewUp(s)
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: ctrl+p did not open the palette over the agents view: %v\n%s", err, term.Snapshot())
	}
	if got := ckCursorRow(term.Screen()); got != cursor {
		t.Fatalf("ASSERTION: ctrl+p moved the list cursor from %q to %q\n%s", cursor, got, term.Snapshot())
	}
	// Keys typed into the palette are the palette's: j and k are its query.
	if err := term.Type("jk"); err != nil {
		t.Fatalf("type into the palette: %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	saveCockpitFrames(t, term, "global-keys-1-palette")
	if got := ckCursorRow(term.Screen()); got != cursor {
		t.Fatalf("ASSERTION: keys typed into the palette moved the list cursor from %q to %q\n%s", cursor, got, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("press esc: %v", err)
	}
	waitCkBack(t, term, paletteTitle, cursor, "esc in the palette")

	// A click on the view outside the palette closes the palette and does
	// not select the row under the pointer.
	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("press ctrl+p: %v", err)
	}
	waitPaletteOpen(t, term, "for the click")
	row, col := findText(t, term, "release-notes")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	waitCkBack(t, term, paletteTitle, cursor, "a click on the view under the palette")
	time.Sleep(400 * time.Millisecond)
	if got := ckCursorRow(term.Screen()); got != cursor {
		t.Fatalf("ASSERTION: a click under the palette selected %q\n%s", got, term.Snapshot())
	}

	// Help, over the view.
	if err := term.SendKeys("?"); err != nil {
		t.Fatalf("press ?: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), helpTitle) && ckViewUp(s)
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: ? did not open help over the agents view: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "global-keys-2-help")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("press esc: %v", err)
	}
	waitCkBack(t, term, helpTitle, cursor, "esc in help")

	// Settings, through the leader chord, over the view.
	if err := term.SendKeys(tuitest.Ctrl('b'), ","); err != nil {
		t.Fatalf("press ctrl+b ,: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "Settings") && ckViewUp(s)
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: ctrl+b , did not open settings over the agents view: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "global-keys-3-settings")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("press esc: %v", err)
	}
	waitCkBack(t, term, "Settings", cursor, "esc in settings")

	// A palette row that goes to another workspace closes the view, which
	// then opens again on the same agent.
	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("press ctrl+p: %v", err)
	}
	waitPaletteOpen(t, term, "for a workspace")
	if err := term.Type("Workspace 2"); err != nil {
		t.Fatalf("type a query: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("press enter: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !ckViewUp(s) && strings.Contains(s.Text(), "to go back to the agents")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a palette row that left for another workspace kept the view over it: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the view again: %v", err)
	}
	waitCkBack(t, term, paletteTitle, cursor, "the view opened again after the palette left it")

	// The reply editor keeps its draft across the palette.
	if err := term.SendKeys("i"); err != nil {
		t.Fatalf("press i: %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	if err := term.Type("draft kept"); err != nil {
		t.Fatalf("type a draft: %v", err)
	}
	if err := term.WaitForText("draft kept", uiTimeout); err != nil {
		t.Fatalf("the draft never showed: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("press ctrl+p: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), paletteTitle) && ckViewUp(s)
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: ctrl+p in the reply editor did not open the palette: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "global-keys-4-palette-over-draft")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("press esc: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), paletteTitle) && strings.Contains(s.Text(), "draft kept")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the draft did not survive the palette: %v\n%s", err, term.Snapshot())
	}
	// The editor still has the keys: what is typed now joins the draft.
	if err := term.Type(" here"); err != nil {
		t.Fatalf("type more: %v", err)
	}
	if err := term.WaitForText("draft kept here", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the editor lost the keys after the palette: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("press esc: %v", err)
	}
	// The key bar is the list's again once the editor is closed.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "ctrl+z undo") && strings.Contains(s.Text(), "esc close")
	}, uiTimeout); err != nil {
		t.Fatalf("esc did not close the reply editor: %v\n%s", err, term.Snapshot())
	}

	// A sheet that takes every key: the palette opens over it too, and esc
	// in the palette leaves the sheet open with its task.
	if err := term.SendKeys("n"); err != nil {
		t.Fatalf("press n: %v", err)
	}
	if err := term.WaitForText("What should the agent do?", uiTimeout); err != nil {
		t.Fatalf("n did not open the New agent sheet: %v\n%s", err, term.Snapshot())
	}
	if err := term.Type("sheet task"); err != nil {
		t.Fatalf("type a task: %v", err)
	}
	if err := term.WaitForText("sheet task", uiTimeout); err != nil {
		t.Fatalf("the task never showed: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("press ctrl+p: %v", err)
	}
	waitPaletteOpen(t, term, "over the New agent sheet")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("press esc: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), paletteTitle) && containsAll(s.Text(), "sheet task", "Start agent")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: esc in the palette did not leave the New agent sheet as it was: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("press esc: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Start agent") && ckViewUp(s)
	}, uiTimeout); err != nil {
		t.Fatalf("esc did not close the New agent sheet: %v\n%s", err, term.Snapshot())
	}

	// Terminal mode: the view lets the leader chord through.
	if err := term.SendKeys("q"); err != nil {
		t.Fatalf("press q: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !ckViewUp(s) }, uiTimeout); err != nil {
		t.Fatalf("q did not close the view: %v\n%s", err, term.Snapshot())
	}
	// Back to the workspace with the pane, which the palette left.
	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("press ctrl+p: %v", err)
	}
	waitPaletteOpen(t, term, "for workspace 1")
	if err := term.Type("Workspace 1"); err != nil {
		t.Fatalf("type a query: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("press enter: %v", err)
	}
	waitPaletteClosed(t, term, "after workspace 1")
	enterTerminalMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the view: %v", err)
	}
	if err := term.WaitFor(ckViewUp, uiTimeout); err != nil {
		t.Fatalf("the view did not open from terminal mode: %v\n%s", err, term.Snapshot())
	}
	// The view binds no comma, so only the prefix table can open settings.
	if err := term.SendKeys(tuitest.Ctrl('b'), ","); err != nil {
		t.Fatalf("press ctrl+b ,: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "Settings") && ckViewUp(s)
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: ctrl+b , in terminal mode did not open settings over the view: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "global-keys-5-settings-terminal-mode")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("press esc: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Settings") && ckViewUp(s)
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: esc in settings did not go back to the view: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after the panels over the agents view")
}

// TestKeybindsDoctorNamesAGlobalKeyOverTheView binds the launcher to ctrl+d,
// which the agents view uses to scroll a detail. The global key acts first
// over the view, so the doctor must name the view's binding as dead. A plain
// character bound globally stays the view's and is not reported.
//
// Negative control, recorded in NEGATIVE_CONTROLS.md: with
// shadowInboxByGlobal doing nothing the doctor reports no conflict.
func TestKeybindsDoctorNamesAGlobalKeyOverTheView(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "[keybindings.global]\nlauncher = [\"ctrl+d\"]\ncommand_palette = [\"ctrl+p\", \"j\"]\n")
	out, err := tuiosCLI(t, base, "keybinds", "doctor")
	if err != nil {
		t.Fatalf("keybinds doctor: %v\n%s", err, out)
	}
	if !strings.Contains(out, "dead: inbox_detail_down [inbox]") {
		t.Fatalf("ASSERTION: the doctor does not say ctrl+d is dead in the agents view\n%s", out)
	}
	if strings.Contains(out, "dead: inbox_down") {
		t.Fatalf("ASSERTION: the doctor says j is dead in the agents view, but a plain character stays the view's\n%s", out)
	}
}
