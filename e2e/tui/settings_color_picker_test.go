package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

func screenBorderInk(s tuitest.Screen) (tuitest.Color, bool) {
	_, rows := s.Size()
	for row := range rows {
		if col := strings.Index(s.Line(row), "╭"); col >= 0 {
			return s.Cell(col, row).Fg, true
		}
	}
	return tuitest.Color{}, false
}

func borderInk(t *testing.T, term *tuitest.Terminal) tuitest.Color {
	t.Helper()
	ink, ok := screenBorderInk(term.Screen())
	if !ok {
		t.Fatalf("no pane corner on screen\n%s", term.Snapshot())
	}
	return ink
}

func findRow(s tuitest.Screen, text string) int {
	_, rows := s.Size()
	for row := range rows {
		if strings.Contains(s.Line(row), text) {
			return row
		}
	}
	return -1
}

func clickSettingsRow(t *testing.T, term *tuitest.Terminal, label string) {
	t.Helper()
	s := term.Screen()
	row := findRow(s, label)
	if row < 0 {
		t.Fatalf("the settings panel drew no row for %q\n%s", label, term.Snapshot())
	}
	col := strings.LastIndex(s.Line(row), "[")
	if col < 0 {
		t.Fatalf("row %q has no bracketed control: %q", label, s.Line(row))
	}
	mouseClick(t, term, col+2, row, tuitest.MouseLeft, 0)
}

func openSettings(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.SendKeys(",", "1"); err != nil {
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
	if err := term.WaitForText("focused border color", uiTimeout); err != nil {
		t.Fatalf("the colour picker did not open: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Tab); err != nil {
		t.Fatalf("tab to the hex field: %v", err)
	}
	if err := term.SendKeys("ff00aa"); err != nil {
		t.Fatalf("type the hex: %v", err)
	}
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

	s := term.Screen()
	row := findRow(s, "quiet")
	if row < 0 {
		t.Fatalf("no keyword row on screen\n%s", term.Snapshot())
	}
	seen := map[tuitest.Color]bool{}
	line := s.Line(row)
	for col := range len(line) {
		if bg := s.Cell(col, row).Bg; bg.Kind != tuitest.ColorDefault {
			seen[bg] = true
		}
	}
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
