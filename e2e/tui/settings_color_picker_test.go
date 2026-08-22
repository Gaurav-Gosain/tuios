package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The colour picker behind the settings panel's colour rows, proved where it has
// to be proved: on the border the user is looking at.
//
// A unit test can show the config field holding "#ff00aa" and the theme package
// handing that colour back, and still be true of a build where the border is
// drawn from something else entirely. The only honest question is what colour
// the terminal was given for the border cell, so that is what this asks.

// screenBorderInk is the colour a screen draws the first pane corner in, and
// whether there was one to read.
//
// It reports rather than fails because the wait predicates below call it, and
// those run off the test goroutine: a t.Fatalf there is a Goexit on the wrong
// stack, which takes the binary down instead of failing the test. There are also
// frames mid-transition with no corner on screen at all, which is a "not yet"
// rather than a failure.
func screenBorderInk(s tuitest.Screen) (tuitest.Color, bool) {
	_, rows := s.Size()
	for row := range rows {
		if col := strings.Index(s.Line(row), "╭"); col >= 0 {
			return s.Cell(col, row).Fg, true
		}
	}
	return tuitest.Color{}, false
}

// borderInk is screenBorderInk for the test goroutine, where a missing corner
// really is a failure.
func borderInk(t *testing.T, term *tuitest.Terminal) tuitest.Color {
	t.Helper()
	ink, ok := screenBorderInk(term.Screen())
	if !ok {
		t.Fatalf("no pane corner on screen\n%s", term.Snapshot())
	}
	return ink
}

// findRow is the screen row carrying text, or -1.
func findRow(s tuitest.Screen, text string) int {
	_, rows := s.Size()
	for row := range rows {
		if strings.Contains(s.Line(row), text) {
			return row
		}
	}
	return -1
}

// clickSettingsRow clicks the named settings row on its control, which is where
// a user aiming at a colour would click.
func clickSettingsRow(t *testing.T, term *tuitest.Terminal, label string) {
	t.Helper()
	s := term.Screen()
	row := findRow(s, label)
	if row < 0 {
		t.Fatalf("the settings panel drew no row for %q\n%s", label, term.Snapshot())
	}
	// The value sits at the right-hand end of the row, inside its brackets.
	col := strings.LastIndex(s.Line(row), "[")
	if col < 0 {
		t.Fatalf("row %q has no bracketed control: %q", label, s.Line(row))
	}
	mouseClick(t, term, col+2, row, tuitest.MouseLeft, 0)
}

// openSettings opens the settings panel and waits for the colour rows.
func openSettings(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.SendKeys(","); err != nil {
		t.Fatalf("open settings: %v", err)
	}
	if err := term.WaitForText("Focused border color", uiTimeout); err != nil {
		t.Fatalf("the settings panel never showed the colour rows: %v\n%s", err, term.Snapshot())
	}
}

// TestColourPickerRecolorsTheBorderOnScreen walks the whole affordance: a click
// on the colour row opens the picker, a hex typed into it previews, applying it
// recolours the border, and clearing puts the theme's colour back. Every
// assertion is a cell colour read off the terminal.
func TestColourPickerRecolorsTheBorderOnScreen(t *testing.T) {
	term, _ := start(t, startOpts{env: []string{"COLORTERM=truecolor"}})
	waitBoot(t, term)
	newWindow(t, term)

	before := borderInk(t, term)
	if before.Kind != tuitest.ColorRGB {
		t.Fatalf("the border is not drawn in an RGB colour (%+v); this test cannot tell it apart from the one it picks", before)
	}

	openSettings(t, term)
	clickSettingsRow(t, term, "Focused border color")
	// The dialog names the row it was opened on, which is also how this knows the
	// picker is up rather than the row's old inline text editor.
	if err := term.WaitForText("focused border color", uiTimeout); err != nil {
		t.Fatalf("the colour picker did not open: %v\n%s", err, term.Snapshot())
	}

	// Tab reaches the hex field from the grid, which is where the picker opens.
	if err := term.SendKeys(tuitest.Tab); err != nil {
		t.Fatalf("tab to the hex field: %v", err)
	}
	if err := term.SendKeys("ff00aa"); err != nil {
		t.Fatalf("type the hex: %v", err)
	}
	// The readout previews the colour before it is applied, so it carries the hex
	// twice: once in the field and once beside the swatch it would become.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Count(s.Text(), "#ff00aa") >= 2
	}, uiTimeout); err != nil {
		t.Fatalf("the picker never previewed the typed colour: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := term.WaitForText("#ff00aa", uiTimeout); err != nil {
		t.Fatalf("the row does not carry the applied colour: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("close settings: %v", err)
	}

	want := tuitest.Color{Kind: tuitest.ColorRGB, R: 0xff, G: 0x00, B: 0xaa}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		ink, ok := screenBorderInk(s)
		return ok && ink == want
	}, uiTimeout); err != nil {
		t.Fatalf("the border is %+v, want the picked %+v: %v\n%s",
			borderInk(t, term), want, err, term.Snapshot())
	}

	// Clearing is the third state, and the one a picker that could only set a
	// value would have taken away: the border goes back to the theme's colour.
	openSettings(t, term)
	clickSettingsRow(t, term, "Focused border color")
	if err := term.WaitForText("focused border color", uiTimeout); err != nil {
		t.Fatalf("the picker did not reopen: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("x"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if err := term.WaitForText("(theme)", uiTimeout); err != nil {
		t.Fatalf("the row does not read as unset after clearing: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("close settings: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		ink, ok := screenBorderInk(s)
		return ok && ink == before
	}, uiTimeout); err != nil {
		t.Fatalf("after clearing the border is %+v, want the theme's %+v: %v\n%s",
			borderInk(t, term), before, err, term.Snapshot())
	}
}

// TestTintPickerOffersItsKeywords covers the option whose literal form the panel
// could not reach at all: the tint cycled three keywords and had no way to a hex.
// Now it opens the same picker, with the keywords drawn as chips in the colour
// each one produces.
func TestTintPickerOffersItsKeywords(t *testing.T) {
	term, _ := start(t, startOpts{env: []string{"COLORTERM=truecolor"}})
	waitBoot(t, term)
	newWindow(t, term)

	openSettings(t, term)
	clickSettingsRow(t, term, "Scrollbar tint")
	if err := term.WaitForText("scrollbar tint", uiTimeout); err != nil {
		t.Fatalf("the tint's picker did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "quiet", "border", "muted")
	}, uiTimeout); err != nil {
		t.Fatalf("the picker does not offer the tint's keywords: %v\n%s", err, term.Snapshot())
	}

	// The chips are painted, which is the whole reason they beat a cycler: the
	// difference between "border" and "muted" is a colour, and the row shows it.
	s := term.Screen()
	row := findRow(s, "quiet")
	if row < 0 {
		t.Fatalf("no keyword row on screen\n%s", term.Snapshot())
	}
	// Any colour kind counts. This terminal is a 256-colour one, so the picker
	// steps its swatches down to palette entries on the way out (and says so
	// beside the hex); insisting on RGB here would be testing the terminal.
	seen := map[tuitest.Color]bool{}
	line := s.Line(row)
	for col := range len(line) {
		if bg := s.Cell(col, row).Bg; bg.Kind != tuitest.ColorDefault {
			seen[bg] = true
		}
	}
	// One per keyword plus the dialog's own ground.
	if len(seen) < 4 {
		t.Errorf("the keyword row paints %d distinct backgrounds, want a swatch per keyword: %v\n%s",
			len(seen), seen, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("close settings: %v", err)
	}
}
