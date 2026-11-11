package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// aggregateTitle is the header the all-windows list draws, and aggregateHint
// the one string on screen that only the open list produces. The list is the
// go-to picker filtered to this session's panes.
const (
	aggregateTitle = "Go to"
	aggregateHint  = "@ # !"
)

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
	if err := term.SendKeys("All windows"); err != nil {
		t.Fatalf("type palette query: %v", err)
	}
	if err := term.WaitForText("All windows", uiTimeout); err != nil {
		t.Fatalf("palette never filtered to the aggregate entry: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("activate palette entry: %v", err)
	}
	// Waited for on the picker's own hint rather than on its title. The title is
	// the word "Windows", which the palette row that opens it also contains, so
	// waiting on the title returned while the palette was still on screen and
	// every measurement below then described the palette instead.
	//
	// A screen too short for the footer drops the hint, so there the picker is
	// known by its title on a line of its own and its position readout.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		if strings.Contains(text, paletteTitle) {
			return false
		}
		return strings.Contains(text, aggregateHint) || (aggregateTitleRow(s) >= 0 && strings.Contains(text, " of "))
	}, uiTimeout); err != nil {
		t.Fatalf("the picker did not open: %v\n%s", err, term.Snapshot())
	}
}

// aggregateTitleRow is the screen row whose only text is the picker's title,
// or -1. The palette row that opens the picker holds more than the title.
func aggregateTitleRow(s tuitest.Screen) int {
	_, height := s.Size()
	for r := range height {
		if strings.TrimSpace(s.Line(r)) == aggregateTitle {
			return r
		}
	}
	return -1
}

// panelExtent measures the block of rows the picker occupies, by the rows
// between its title and its hint line. It is the measurement the redesign is
// about: the old overlay took three quarters of the screen whatever it held.
// On a screen too short for the hint, the position readout is the last line
// measured.
func panelExtent(t *testing.T, term *tuitest.Terminal) (rows int) {
	t.Helper()
	s := term.Screen()
	_, height := s.Size()
	bottom := -1
	for r := range height {
		if strings.Contains(s.Line(r), aggregateHint) {
			bottom = r
		}
	}
	if bottom < 0 {
		for r := range height {
			if strings.Contains(s.Line(r), " of ") {
				bottom = r
			}
		}
	}
	if bottom < 0 {
		t.Fatalf("the picker's hint line and readout are not on screen\n%s", term.Snapshot())
	}
	// The nearest title above the hint, which with the palette gone is the
	// picker's own.
	for r := bottom; r >= 0; r-- {
		if strings.Contains(s.Line(r), aggregateTitle) {
			return bottom - r + 1
		}
	}
	t.Fatalf("the picker's title is not above its hint\n%s", term.Snapshot())
	return 0
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
		return screenHas(s, aggregateHint, "ALPHA", "BRAVO")
	}, uiTimeout); err != nil {
		t.Fatalf("the picker did not list both windows: %v\n%s", err, term.Snapshot())
	}

	// Click the row for the window that is not the focused one.
	col, row := findOnScreen(t, term, "ALPHA")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)

	// The click closes the picker and lands on that window, which is what Enter
	// on the same row does.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), aggregateHint)
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

	// Ten, so the list cannot possibly fit: a shorter one legitimately does, and
	// then there is nothing to scroll and no readout to look for.
	for range 10 {
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

// TestAllWindowsShowsAndFindsEachPanesFolder: a pane row in go-to ends with
// the pane's working directory, as the old window list did, and typing a
// folder name keeps the panes in it.
//
// How this could pass wrongly, written down first:
//   - The folder could be read off the rail's files section or a title bar
//     rather than the row. It is looked for only on the row whose
//     breadcrumb ends in the pane's name ("› north"), which the rail and the
//     title bars never draw.
//   - The filter could keep north by its name, not its folder. The word typed
//     is in north's folder and in neither pane's name.
//   - The filter could keep every row. south's folder lacks the word, and its
//     row has to go.
//   - Both panes could share one folder and show it by chance. Each is opened
//     in a folder of its own.
func TestAllWindowsShowsAndFindsEachPanesFolder(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "e2e-dirs", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	for _, p := range []struct{ name, dir string }{{"north", "cwd-kestrel"}, {"south", "cwd-osprey"}} {
		dir := filepath.Join(base, p.dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		if out, err := tuiosCLI(t, base, "new-window", "-s", "e2e-dirs", p.name, "--cwd", dir, "--no-focus"); err != nil {
			t.Fatalf("open %s: %v\n%s", p.name, err, out)
		}
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-dirs"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 3 }, bootTimeout); err != nil {
		t.Fatalf("the client never drew the session's three panes: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)

	rowOf := func(s tuitest.Screen, name string) string {
		_, rows := s.Size()
		for r := range rows {
			if line := s.Line(r); strings.Contains(line, "› "+name) {
				return line
			}
		}
		return ""
	}
	openAggregateView(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(rowOf(s, "north"), "kestrel") && strings.Contains(rowOf(s, "south"), "osprey")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the pane rows do not end with their folders: %v\n%s", err, term.Snapshot())
	}
	dir := artifactDir(t)
	saveArtifact(t, term, dir, "all-windows")
	savePNG(t, term.Screen(), hostPalette(t, ""), dir, "all-windows")

	if err := term.SendKeys("kestrel"); err != nil {
		t.Fatalf("type the folder name: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(rowOf(s, "north"), "kestrel") && rowOf(s, "south") == ""
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: typing north's folder did not keep north alone: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "all-windows-filtered")
	alive(t, term, "after filtering go-to by a folder")
}
