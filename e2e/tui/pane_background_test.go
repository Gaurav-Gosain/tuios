package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The pane background, set where a user sets it: the settings panel's colour
// row and its picker. Every assertion is a cell colour read off the terminal,
// because the question is what the terminal was told to draw behind the pane,
// and a unit test of the config field cannot answer it.

// paneBgMarker is text only the shell's printf output carries: the typed
// command splits it with a %s, so the echo of the keystrokes never matches.
const paneBgMarker = "REDMARK"

// paneBgCells finds the marker row and returns the cells the test reads: the
// program's own red cell, a default-background cell on the same row past the
// text, and the pane border on that row when the pane has one.
func paneBgCells(s tuitest.Screen) (red, blank, border tuitest.Cell, hasBorder, ok bool) {
	row := findRow(s, paneBgMarker)
	if row < 0 {
		return red, blank, border, false, false
	}
	line := s.Line(row)
	col := strings.Index(line, paneBgMarker)
	// Line is text, so a column is a rune count and not a byte offset.
	col = len([]rune(line[:col]))
	red = s.Cell(col, row)
	blank = s.Cell(col+len(paneBgMarker)+6, row)
	runes := []rune(line)
	for c := col - 1; c >= 0; c-- {
		if runes[c] == '│' || runes[c] == '┃' {
			border, hasBorder = s.Cell(c, row), true
			break
		}
	}
	return red, blank, border, hasBorder, true
}

func TestPaneBackgroundFromTheSettingsPanel(t *testing.T) {
	term, _ := start(t, startOpts{env: []string{"COLORTERM=truecolor"}})
	waitBoot(t, term)
	newWindow(t, term)

	enterTerminalMode(t, term)
	runInShell(t, term, `printf '\033[41mRED%s\033[0m\n' MARK`, paneBgMarker, 10*time.Second)
	leaveTerminalMode(t, term)

	red, blank, _, _, ok := paneBgCells(term.Screen())
	if !ok {
		t.Fatalf("the printf output is not on screen\n%s", term.Snapshot())
	}
	if blank.Bg.Kind != tuitest.ColorDefault {
		t.Fatalf("with the option off a blank pane cell has bg %+v, want the terminal's own\n%s", blank.Bg, term.Snapshot())
	}
	if red.Bg.Kind == tuitest.ColorDefault {
		t.Fatalf("the program's red cell has no background; the fixture is wrong\n%s", term.Snapshot())
	}
	programRed := red.Bg

	openSettings(t, term)
	clickSettingsRow(t, term, "Pane background")
	if err := term.WaitForText("pane background", uiTimeout); err != nil {
		t.Fatalf("the colour picker did not open on the pane background row: %v\n%s", err, term.Snapshot())
	}
	// The keywords are offered beside the grid, like the scrollbar tint's.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "off", "theme")
	}, uiTimeout); err != nil {
		t.Fatalf("the picker does not offer off and theme: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Tab); err != nil {
		t.Fatalf("tab to the hex field: %v", err)
	}
	if err := term.SendKeys("123456"); err != nil {
		t.Fatalf("type the hex: %v", err)
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := term.WaitForText("#123456", uiTimeout); err != nil {
		t.Fatalf("the row does not carry the applied colour: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("close settings: %v", err)
	}

	want := tuitest.Color{Kind: tuitest.ColorRGB, R: 0x12, G: 0x34, B: 0x56}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, blank, _, _, ok := paneBgCells(s)
		return ok && blank.Bg == want
	}, uiTimeout); err != nil {
		t.Fatalf("a blank pane cell never took the picked background %+v: %v\n%s", want, err, term.Snapshot())
	}
	red, _, border, hasBorder, _ := paneBgCells(term.Screen())
	if red.Bg != programRed {
		t.Errorf("the program's red cell became %+v, want its own %+v", red.Bg, programRed)
	}
	if hasBorder && border.Bg.Kind != tuitest.ColorDefault {
		t.Errorf("the pane border was painted %+v; the border is chrome and keeps the terminal's background", border.Bg)
	}
	if !hasBorder {
		t.Log("no border glyph on the marker row, so the border was not checked")
	}

	// Clearing puts the pane back on the terminal's own background.
	openSettings(t, term)
	clickSettingsRow(t, term, "Pane background")
	if err := term.WaitForText("pane background", uiTimeout); err != nil {
		t.Fatalf("the picker did not reopen: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("x"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if err := term.WaitForText("(off)", uiTimeout); err != nil {
		t.Fatalf("the row does not read as off after clearing: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("close settings: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, blank, _, _, ok := paneBgCells(s)
		return ok && blank.Bg.Kind == tuitest.ColorDefault
	}, uiTimeout); err != nil {
		t.Fatalf("after clearing a blank pane cell kept a background: %v\n%s", err, term.Snapshot())
	}
}
