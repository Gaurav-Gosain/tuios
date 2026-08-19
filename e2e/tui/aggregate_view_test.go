package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// aggregateTitle is the header the all-windows picker draws.
const aggregateTitle = "Windows"

// openAggregateView opens the all-windows picker through the command palette,
// which is the only way in: it has no default keybinding.
func openAggregateView(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.SendKeys(tuitest.Ctrl('p')); err != nil {
		t.Fatalf("open palette: %v", err)
	}
	if err := term.WaitForText(paletteTitle, uiTimeout); err != nil {
		t.Fatalf("palette did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("All Windows"); err != nil {
		t.Fatalf("type palette query: %v", err)
	}
	if err := term.WaitForText("All Windows", uiTimeout); err != nil {
		t.Fatalf("palette never filtered to the aggregate entry: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("activate palette entry: %v", err)
	}
	if err := term.WaitForText(aggregateTitle, uiTimeout); err != nil {
		t.Fatalf("the picker did not open: %v\n%s", err, term.Snapshot())
	}
}

// panelExtent measures the block of rows the picker occupies, by the rows
// between its title and its hint line. It is the measurement the redesign is
// about: the old overlay took three quarters of the screen whatever it held.
func panelExtent(t *testing.T, term *tuitest.Terminal) (rows int) {
	t.Helper()
	s := term.Screen()
	_, height := s.Size()
	top, bottom := -1, -1
	for r := range height {
		line := s.Line(r)
		if top < 0 && strings.Contains(line, aggregateTitle) {
			top = r
		}
		if strings.Contains(line, "esc close") {
			bottom = r
		}
	}
	if top < 0 || bottom < 0 {
		t.Fatalf("could not find the picker's extent\n%s", term.Snapshot())
	}
	return bottom - top + 1
}

// TestAggregateViewSizesToItsContent is the reported complaint, measured. The
// overlay used to be four fifths of the screen wide by three quarters tall
// whatever it listed, so a session with one window got a mostly empty modal.
// A picker holding one row must be shorter than one holding many.
func TestAggregateViewSizesToItsContent(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)

	openAggregateView(t, term)
	small := panelExtent(t, term)
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("close picker: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	for range 8 {
		newWindow(t, term)
	}
	openAggregateView(t, term)
	large := panelExtent(t, term)

	if small >= large {
		t.Errorf("the picker is %d rows for an empty session and %d rows for eight windows; it is not sizing to its content\n%s",
			small, large, term.Snapshot())
	}
	// The screen is forty rows tall. A picker holding nothing that still takes
	// most of them is the bug being fixed, whatever the comparison above says.
	if small > 12 {
		t.Errorf("an empty picker is %d rows tall\n%s", small, term.Snapshot())
	}

	alive(t, term, "after opening the aggregate view")
}

// TestAggregateViewRowClickJumpsToThatWindow: the one overlay whose entire
// purpose is picking a window returned no hit rows at all, so it was the one
// list in tuios a click did nothing to. A click has to mean what Enter means.
func TestAggregateViewRowClickJumpsToThatWindow(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)

	newWindow(t, term)
	renameWindow(t, term, "ALPHA")
	newWindow(t, term)
	renameWindow(t, term, "BRAVO")

	openAggregateView(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, aggregateTitle, "ALPHA", "BRAVO")
	}, uiTimeout); err != nil {
		t.Fatalf("the picker did not list both windows: %v\n%s", err, term.Snapshot())
	}

	// Click the row for the window that is not the focused one.
	col, row := findOnScreen(t, term, "ALPHA")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)

	// The click closes the picker and lands on that window, which is what Enter
	// on the same row does.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), aggregateTitle)
	}, uiTimeout); err != nil {
		t.Fatalf("clicking a row left the picker open: %v\n%s", err, term.Snapshot())
	}
	if !strings.Contains(term.Screen().Text(), "ALPHA") {
		t.Errorf("the click did not land on the window its row named\n%s", term.Snapshot())
	}

	alive(t, term, "after clicking an aggregate view row")
}

// TestAggregateViewFitsAShortScreen: the panel trims itself to the screen it is
// drawn on, so a terminal too short for the whole list still shows a usable
// picker rather than one running off the bottom.
func TestAggregateViewFitsAShortScreen(t *testing.T) {
	term, _ := start(t, startOpts{cols: 60, rows: 14})
	waitBoot(t, term)

	for range 5 {
		newWindow(t, term)
	}
	openAggregateView(t, term)

	rows := panelExtent(t, term)
	if rows > 14 {
		t.Errorf("the picker is %d rows tall on a 14-row screen\n%s", rows, term.Snapshot())
	}
	// A list it cannot show whole says so, the way every other list overlay does.
	if !strings.Contains(term.Screen().Text(), " of ") {
		t.Errorf("a scrolled picker showed no position readout\n%s", term.Snapshot())
	}

	alive(t, term, "after opening the aggregate view on a short screen")
}
