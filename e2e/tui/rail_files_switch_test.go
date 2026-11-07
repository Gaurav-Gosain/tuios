package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The footer's files control is a switch. A person whose layout leaves the
// files section out clicked it once by accident, the section came up, and the
// control looked the same as before: nothing on the rail said why the section
// was there or how to take it off again.

// railFilesSwitchConfig is the person's layout: a left rail 28 columns wide
// with no files section.
const railFilesSwitchConfig = "[appearance.sidebar]\nenabled = true\nposition = \"left\"\nwidth = 28\n" +
	"sections = \"sessions:20,terminals,agents\"\n"

// footerFilesAt is the screen row of the footer's files control, the last
// rail row that carries the word, and its first column. ok is false when the
// footer draws no such control.
func footerFilesAt(s tuitest.Screen) (col, row int, ok bool) {
	_, rows := s.Size()
	for r := rows - 1; r >= 0; r-- {
		if c := strings.Index(railLine(s, r), "files"); c >= 0 {
			return c, r, true
		}
	}
	return 0, 0, false
}

// footerFilesOn reports whether the footer's files control draws as on: every
// cell of the word underlined. The underline is the attribute that reads at
// every colour depth, so the test asks for it rather than for a colour.
func footerFilesOn(s tuitest.Screen) bool {
	col, row, ok := footerFilesAt(s)
	if !ok {
		return false
	}
	for c := col; c < col+len("files"); c++ {
		if !s.Cell(c, row).Underline {
			return false
		}
	}
	return true
}

// filesHeaderRow is the screen row of the files section's header, or -1. It
// is the first rail row above the footer that starts with the section's label.
func filesHeaderRow(s tuitest.Screen) int {
	_, footer, ok := footerFilesAt(s)
	if !ok {
		return -1
	}
	for r := range footer {
		line := strings.TrimSpace(railLine(s, r))
		if strings.HasPrefix(strings.ToLower(line), "files") {
			return r
		}
	}
	return -1
}

// filesHeaderClose is the column of the close control at the right end of the
// files header, or -1 when the header has none.
func filesHeaderClose(s tuitest.Screen) int {
	r := filesHeaderRow(s)
	if r < 0 {
		return -1
	}
	line := strings.TrimRight(railLine(s, r), " │")
	if !strings.HasSuffix(line, " x") {
		return -1
	}
	return len([]rune(line)) - 1
}

// TestRailFilesSwitchShowsItsState clicks the footer's files control on a
// rail whose layout leaves files out. While the section is on, the control
// draws as on and the header offers a close control. A second click on the
// control, and a click on the close control, each take the section off and
// put the control back to off.
//
// Negative control, confirmed red: with the on state in sidebarFooter drawn
// as the plain muted label again, the wait for the on state times out.
func TestRailFilesSwitchShowsItsState(t *testing.T) {
	term, _ := railClient(t, "e2e", railFilesSwitchConfig, startOpts{cols: 120, rows: 40})

	if filesHeaderRow(term.Screen()) >= 0 {
		t.Fatalf("the files section is on the rail before any click:\n%s", term.Snapshot())
	}
	if footerFilesOn(term.Screen()) {
		t.Fatalf("the files control draws as on before any click:\n%s", term.Snapshot())
	}
	copiesFrame(t, term, "rail-files-switch-off")

	clickFooterFiles := func(why string) {
		t.Helper()
		col, row, ok := footerFilesAt(term.Screen())
		if !ok {
			t.Fatalf("%s: the rail drew no files control:\n%s", why, term.Snapshot())
		}
		mouseClick(t, term, col+1, row, tuitest.MouseLeft, 0)
	}
	waitOn := func(why string) {
		t.Helper()
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return filesHeaderRow(s) >= 0 && footerFilesOn(s) && filesHeaderClose(s) >= 0
		}, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: %s: the section, the on control or the close control is missing: %v\n%s",
				why, err, term.Snapshot())
		}
	}
	waitOff := func(why string) {
		t.Helper()
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			_, _, ok := footerFilesAt(s)
			return ok && filesHeaderRow(s) < 0 && !footerFilesOn(s)
		}, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: %s: the section or the on control is still there: %v\n%s",
				why, err, term.Snapshot())
		}
	}

	clickFooterFiles("first click")
	waitOn("after the first click on the footer control")
	copiesFrame(t, term, "rail-files-switch-on")

	clickFooterFiles("second click")
	waitOff("after the second click on the footer control")

	// The close control in the header is the other way out.
	clickFooterFiles("third click")
	waitOn("after the footer control turned the section on again")
	s := term.Screen()
	mouseClick(t, term, filesHeaderClose(s), filesHeaderRow(s), tuitest.MouseLeft, 0)
	waitOff("after a click on the header's close control")

	alive(t, term, "after turning the files section on and off")
}
