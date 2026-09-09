package tuie2e

import (
	"slices"
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
// trimRowEdge drops the trailing spaces and the pane's right border from a
// row. A tiled pane reaches the last column, so its border sits after the
// picker's own text and would otherwise read as the row's last field.
func trimRowEdge(line string) string {
	return strings.TrimRight(line, " \u2502\u2503|")
}

// pickerSpan finds the columns the picker panel occupies.
//
// The picker previews the effect it is highlighting, and the preview animates
// over the whole screen, not only behind the panel. Particles therefore land in
// a row's text on both sides of the panel, and the row's last field stops being
// the duration band. Reading a whole screen line is only safe while the preview
// has not drawn yet, which is why this test passed locally and failed under
// load in CI.
//
// The panel's own divider is the anchor: it is the longest unbroken run of the
// horizontal box glyph on screen, and the settings panel behind it draws a
// shorter one. Its span is the panel's content columns.
func pickerSpan(s tuitest.Screen) (start, end int, ok bool) {
	_, height := s.Size()
	lines := make([]string, height)
	for row := range height {
		lines[row] = s.Line(row)
	}
	return pickerSpanLines(lines)
}

// pickerSpanLines is pickerSpan over plain lines, so the rule can be tested
// against a screen captured from a real run.
func pickerSpanLines(lines []string) (start, end int, ok bool) {
	for _, raw := range lines {
		line := []rune(raw)
		for i := 0; i < len(line); {
			if line[i] != '\u2500' {
				i++
				continue
			}
			j := i
			for j < len(line) && line[j] == '\u2500' {
				j++
			}
			if j-i > end-start {
				start, end, ok = i, j, true
			}
			i = j
		}
	}
	return start, end, ok
}

// maskToPicker blanks everything outside the picker's columns, so the preview's
// particles cannot be read as a row's fields. It blanks rather than cuts,
// because the caller turns the row's text back into a screen column with
// strings.Index and a cut line would shift every one of those.
func maskToPicker(line string, start, end int) string {
	r := []rune(line)
	for i := range r {
		if i < start || i >= end {
			r[i] = ' '
		}
	}
	return trimRowEdge(string(r))
}

func effectNameIn(line string) string {
	fields := strings.Fields(trimRowEdge(line))
	if len(fields) < 2 {
		return ""
	}
	return fields[len(fields)-2]
}

func effectRowsOnScreen(s tuitest.Screen) (rows []int, text []string) {
	start, end, ok := pickerSpan(s)
	_, height := s.Size()
	for row := range height {
		line := trimRowEdge(s.Line(row))
		if ok {
			line = maskToPicker(s.Line(row), start, end)
		}
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

// TestEffectRowDetectorReadsABorderedRow pins the shape the row helpers must
// cope with. A tiled pane draws its right border in the last column, so the
// picker's rows arrive with that border after the band word. The helpers read
// the band and the name from the end of the row, so an untrimmed border makes
// every row unrecognisable and the picker looks empty while it is on screen.
func TestEffectRowDetectorReadsABorderedRow(t *testing.T) {
	const bordered = "│                   `                   › Effect               binarypath                                          long                                        │"

	if got := effectNameIn(bordered); got != "binarypath" {
		t.Errorf("effectNameIn read %q, want %q", got, "binarypath")
	}

	fields := strings.Fields(trimRowEdge(bordered))
	last := fields[len(fields)-1]
	if !slices.Contains(effectBands, last) {
		t.Errorf("the row's last field is %q, which is not one of the bands %v", last, effectBands)
	}
}

// TestEffectRowSurvivesTheLivePreview pins the row helpers against a screen
// captured from a real CI run, where the picker's own preview was animating.
//
// The picker previews the effect it highlights, and the preview draws over the
// whole screen. Particles land in a row's text on both sides of the panel, so
// the row's last field is a particle and not the duration band. Before this was
// handled, the list read as empty while it was plainly on screen, and the test
// waited ten seconds for rows it could already see.
func TestEffectRowSurvivesTheLivePreview(t *testing.T) {
	// Captured from run 34313515028, trimmed to the rows that matter.
	lines := []string{
		"                       ¤   •                                                                                                                ¤                    °•",
		"                                                 Appearance Sideba   󰤄 Screen saver effect                                           ¤",
		"              ¤   •                   *`   '       Enabled            ──────────────────────────────────────────────────────────",
		"                         °                  •    › Effect               binarypath                                          long       *           ·              ¤",
		"                              °                    While busy           blackhole                                           long",
	}

	start, end, ok := pickerSpanLines(lines)
	if !ok {
		t.Fatal("the picker's divider was not found, so its columns are unknown")
	}

	const wantName = "binarypath"
	row := maskToPicker(lines[3], start, end)
	if got := effectNameIn(row); got != wantName {
		t.Errorf("effectNameIn read %q, want %q", got, wantName)
	}

	fields := strings.Fields(row)
	if len(fields) == 0 {
		t.Fatal("the masked row carries no fields")
	}
	if last := fields[len(fields)-1]; !slices.Contains(effectBands, last) {
		t.Errorf("the masked row's last field is %q, which is not one of the bands %v", last, effectBands)
	}
}
