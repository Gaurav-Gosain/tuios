package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The saver submenu, proved where it was reported: on the screen, with real
// mouse reports, against a real tuios.
//
// The picker opens over the settings panel and overlaps it. Both panels sat on
// the same z-index, so the hit test broke the tie on whichever was recorded
// first and every click inside the picker reached the settings row behind it.

const (
	// saverSectionMark is a row only the Saver section carries.
	saverSectionMark = "While busy"
	// effectRowMark is the Effect row's help text. The settings panel prints
	// the help of the selected row only, so this on screen means that row is
	// the selected one, both on the way in and after the picker closes.
	effectRowMark = "The animation the screen saver runs"
)

// effectBands are the words the picker prints in each row's right-hand column.
// A body line ending in one of them is an effect row, which is how the list is
// found on screen without counting offsets from the panel border.
var effectBands = []string{"none", "short", "medium", "long"}

// effectRowsOnScreen returns the screen rows of the picker's effect list, in
// order, paired with their text.
// effectNameIn returns the effect a picker row names.
//
// The picker is drawn over the settings panel, so one screen row can carry a
// settings label on the left and an effect on the right: "> Effect
// binarypath long". The name is always the field before the duration band,
// never the first field, which is what made this test click the wrong column
// when the fifth match happened to be one of those rows.
func effectNameIn(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return ""
	}
	return fields[len(fields)-2]
}

func effectRowsOnScreen(s tuitest.Screen) (rows []int, text []string) {
	_, height := s.Size()
	for row := range height {
		line := strings.TrimRight(s.Line(row), " ")
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		for _, band := range effectBands {
			if fields[len(fields)-1] == band {
				rows = append(rows, row)
				text = append(text, line)
				break
			}
		}
	}
	return rows, text
}

// selectSaverEffectRow walks the settings panel to the Saver section and leaves
// the Effect row selected, the way the report describes reaching it.
func selectSaverEffectRow(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	openSettings(t, term)
	for range 15 {
		if strings.Contains(term.Screen().Text(), saverSectionMark) {
			break
		}
		if err := term.SendKeys(tuitest.Tab); err != nil {
			t.Fatalf("step to the next settings section: %v", err)
		}
		_ = term.WaitFor(func(s tuitest.Screen) bool {
			return strings.Contains(s.Text(), saverSectionMark)
		}, uiTimeout/10)
	}
	if !strings.Contains(term.Screen().Text(), saverSectionMark) {
		t.Fatalf("the settings panel never reached the Saver section\n%s", term.Snapshot())
	}
	for range 8 {
		if strings.Contains(term.Screen().Text(), effectRowMark) {
			return
		}
		if err := term.SendKeys(tuitest.Down); err != nil {
			t.Fatalf("move to the next settings row: %v", err)
		}
		_ = term.WaitFor(func(s tuitest.Screen) bool {
			return strings.Contains(s.Text(), effectRowMark)
		}, uiTimeout/10)
	}
	t.Fatalf("the Effect row never became the selected one\n%s", term.Snapshot())
}

// TestEffectPickerTakesTheClicksOverTheSettingsPanel is the reported bug: with
// the picker open over the settings panel, a click on one of its rows has to
// apply that effect and leave the settings selection where it was.
func TestEffectPickerTakesTheClicksOverTheSettingsPanel(t *testing.T) {
	term, _ := start(t, startOpts{cols: 160, rows: 45})
	waitBoot(t, term)
	newWindow(t, term)

	selectSaverEffectRow(t, term)
	before := effectRowValue(t, term)
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("open the effect picker: %v", err)
	}
	if err := term.WaitForText("Screen saver effect", uiTimeout); err != nil {
		t.Fatalf("the effect picker did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		rows, _ := effectRowsOnScreen(s)
		return len(rows) >= 5
	}, uiTimeout); err != nil {
		t.Fatalf("the picker drew no effect list: %v\n%s", err, term.Snapshot())
	}

	// The fifth row of the list, well inside the part of the picker that covers
	// the settings panel.
	rows, text := effectRowsOnScreen(term.Screen())
	target, line := rows[4], text[4]
	name := effectNameIn(line)
	if name == "" {
		t.Fatalf("no effect name in the row to click: %q", line)
	}
	if name == before {
		t.Fatalf("the row being clicked already holds the current value %q, so the click proves nothing", before)
	}
	col := strings.Index(line, name)
	if col < 0 {
		t.Fatalf("could not place the name %q in its own row %q", name, line)
	}

	mouseClick(t, term, col, target, tuitest.MouseLeft, 0)

	// A click on a picker row applies it and closes the picker, so the proof is
	// on the settings row behind: it now holds the clicked effect.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Screen saver effect") &&
			strings.Contains(s.Text(), effectRowMark)
	}, uiTimeout); err != nil {
		t.Fatalf("the picker did not take the click: %v\n%s", err, term.Snapshot())
	}
	if got := effectRowValue(t, term); got != name {
		t.Errorf("the Effect row reads %q after a click on the %q row; the click went to the settings panel behind the picker\n%s",
			got, name, term.Snapshot())
	}
	// The settings panel prints the help of its selected row, so this says the
	// selection did not move to the row under Effect.
	if !strings.Contains(term.Screen().Text(), effectRowMark) {
		t.Errorf("the click moved the settings selection off the Effect row, so it landed on the panel behind the picker\n%s",
			term.Snapshot())
	}
}

// effectRowValue reads the value the settings Effect row shows, out of the
// cycler brackets at the right-hand end of the row.
func effectRowValue(t *testing.T, term *tuitest.Terminal) string {
	t.Helper()
	s := term.Screen()
	row := findRow(s, "Effect ")
	if row < 0 {
		t.Fatalf("no Effect row on screen\n%s", term.Snapshot())
	}
	line := strings.TrimRight(s.Line(row), " ")
	open := strings.LastIndex(line, "\u2039")
	closeAt := strings.LastIndex(line, "\u203a")
	if open < 0 || closeAt <= open {
		t.Fatalf("the Effect row has no cycler value: %q", line)
	}
	return strings.TrimSpace(line[open+len("\u2039") : closeAt])
}
