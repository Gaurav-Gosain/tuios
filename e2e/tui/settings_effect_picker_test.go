package tuie2e

import (
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

const (
	saverSectionMark = "While busy"
	effectRowMark    = "The animation the screen saver runs"
)

var effectBands = []string{"none", "short", "medium", "long"}

func trimRowEdge(line string) string {
	return strings.TrimRight(line, " \u2502\u2503|")
}

func pickerSpan(s tuitest.Screen) (start, end int, ok bool) {
	_, height := s.Size()
	lines := make([]string, height)
	for row := range height {
		lines[row] = s.Line(row)
	}
	return pickerSpanLines(lines)
}

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
		if slices.Contains(effectBands, fields[len(fields)-1]) {
			rows = append(rows, row)
			text = append(text, line)
		}
	}
	return rows, text
}

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
	if !strings.Contains(term.Screen().Text(), effectRowMark) {
		t.Errorf("the click moved the settings selection off the Effect row, so it landed on the panel behind the picker\n%s",
			term.Snapshot())
	}
}

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
