package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The backgrounds, set where a user sets them: the settings panel's
// Backgrounds tab and its colour picker. One row paints every surface and one
// override repaints a single surface, and every assertion is a cell colour
// read off the terminal.

// openBackgrounds opens the settings panel on the Backgrounds tab, which is
// the one after Appearance.
func openBackgrounds(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	openSettings(t, term)
	if err := term.SendKeys("]"); err != nil {
		t.Fatalf("next tab: %v", err)
	}
	if err := term.WaitForText("All surfaces", uiTimeout); err != nil {
		t.Fatalf("the Backgrounds tab did not open: %v\n%s", err, term.Snapshot())
	}
}

// pickHex opens the picker on a colour row, types a hex into it and applies
// it, then waits for the row to carry the value.
func pickHex(t *testing.T, term *tuitest.Terminal, row, hex string) {
	t.Helper()
	clickSettingsRow(t, term, row)
	if err := term.WaitForText(strings.ToLower(row), uiTimeout); err != nil {
		t.Fatalf("the colour picker did not open on %q: %v\n%s", row, err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Tab); err != nil {
		t.Fatalf("tab to the hex field: %v", err)
	}
	if err := term.SendKeys(hex); err != nil {
		t.Fatalf("type the hex: %v", err)
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// The picker's own field shows the hex before enter lands, so what is
	// waited for is the row itself carrying it, which only an apply does.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		y := findRow(s, row)
		return y >= 0 && strings.Contains(s.Line(y), "#"+hex)
	}, uiTimeout); err != nil {
		t.Fatalf("the %q row does not carry #%s after apply: %v\n%s", row, hex, err, term.Snapshot())
	}
}

// closeBackgrounds closes the settings panel and waits until it is gone.
func closeBackgrounds(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("close settings: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !screenHas(s, "All surfaces")
	}, uiTimeout); err != nil {
		t.Fatalf("the settings panel did not close: %v\n%s", err, term.Snapshot())
	}
}

func TestBackgroundsFromTheSettingsPanel(t *testing.T) {
	term, _ := start(t, startOpts{env: []string{"COLORTERM=truecolor"}})
	waitBoot(t, term)
	newWindow(t, term)

	enterTerminalMode(t, term)
	runInShell(t, term, `printf '\033[41mRED%s\033[0m\n' MARK`, paneBgMarker, 10*time.Second)
	leaveTerminalMode(t, term)

	red, blank, border, hasBorder, ok := paneBgCells(term.Screen())
	if !ok {
		t.Fatalf("the printf output is not on screen\n%s", term.Snapshot())
	}
	if blank.Bg.Kind != tuitest.ColorDefault {
		t.Fatalf("with every background off a blank pane cell has bg %+v, want the terminal's own\n%s", blank.Bg, term.Snapshot())
	}
	programRed := red.Bg
	borderInk := border.Fg

	openBackgrounds(t, term)
	pickHex(t, term, "All surfaces", "123456")
	pickHex(t, term, "Dock background", "654321")
	closeBackgrounds(t, term)

	all := tuitest.Color{Kind: tuitest.ColorRGB, R: 0x12, G: 0x34, B: 0x56}
	dock := tuitest.Color{Kind: tuitest.ColorRGB, R: 0x65, G: 0x43, B: 0x21}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, blank, _, _, ok := paneBgCells(s)
		return ok && blank.Bg == all
	}, uiTimeout); err != nil {
		t.Fatalf("a blank pane cell never took the All surfaces colour %+v: %v\n%s", all, err, term.Snapshot())
	}

	s := term.Screen()
	red, _, border, hasBorder, _ = paneBgCells(s)
	if red.Bg != programRed {
		t.Errorf("the program's red cell became %+v, want its own %+v", red.Bg, programRed)
	}
	if hasBorder {
		if border.Bg != all {
			t.Errorf("the pane border has bg %+v, want the All surfaces colour %+v", border.Bg, all)
		}
		if border.Fg != borderInk {
			t.Errorf("the pane border's ink changed from %+v to %+v; chrome keeps its own colours", borderInk, border.Fg)
		}
	} else {
		t.Log("no border glyph on the marker row, so the border was not checked")
	}

	// Every cell on the screen is painted now, by All surfaces or by the
	// dock's own colour, or keeps a colour of its own. And the dock's colour
	// is on the dock's rows only: no row carries both.
	lines := strings.Count(term.Snapshot(), "\n")
	dockRows := 0
	var problems []string
	for y := 0; y < 40 && y <= lines; y++ {
		hasAll, hasDock := false, false
		for x := 0; x < 120; x++ {
			c := s.Cell(x, y)
			switch {
			case c.Bg.Kind == tuitest.ColorDefault:
				if len(problems) < 5 {
					problems = append(problems, fmt.Sprintf("(%d,%d) is still on the terminal's background", x, y))
				}
			case c.Bg == all:
				hasAll = true
			case c.Bg == dock:
				hasDock = true
			}
		}
		if hasDock {
			dockRows++
		}
		if hasAll && hasDock {
			problems = append(problems, fmt.Sprintf("row %d carries both colours", y))
		}
	}
	if len(problems) > 0 {
		t.Errorf("%s\n%s", strings.Join(problems, "\n"), term.Snapshot())
	}
	if dockRows == 0 {
		t.Errorf("no row carries the dock's own colour %+v\n%s", dock, term.Snapshot())
	}
}
