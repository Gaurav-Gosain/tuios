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

// railFilesDefaultConfig is the person's rail: the shipped layout, which has
// the files section, on the left at 28 columns.
const railFilesDefaultConfig = "[appearance.sidebar]\nenabled = true\nposition = \"left\"\nwidth = 28\n"

// TestRailFilesSwitchIsSaved: the footer's files control is a switch the
// person sets, so it is saved in the layout. Before, it was this client's
// alone and not saved: a section hidden with it came back on the next client
// and after a daemon restart, because the layout still named it. The test
// hides the section, then attaches a second client, then restarts the daemon
// and attaches again, and the section stays hidden each time. The control
// then puts it back. A section the layout names draws the control plain and
// has no close control, because the switch is not a moment's show.
//
// Negative control, confirmed red: on origin/main, where the switch is this
// client's alone, the second client draws the files section again.
func TestRailFilesSwitchIsSaved(t *testing.T) {
	requireReal(t, "restarts the daemon")
	term, base := railClient(t, "e2e", railFilesDefaultConfig, startOpts{cols: 120, rows: 40})
	cfg := configPathIn(base)

	waitSection := func(term *tuitest.Terminal, on bool, why string) {
		t.Helper()
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			_, _, ok := footerFilesAt(s)
			return ok && (filesHeaderRow(s) >= 0) == on && !footerFilesOn(s) && filesHeaderClose(s) < 0
		}, uiTimeout); err != nil {
			copiesFrame(t, term, "rail-files-switch-failed")
			t.Fatalf("ASSERTION: %s: want the files section on=%v, a plain control and no close control: %v\n%s",
				why, on, err, term.Snapshot())
		}
	}
	clickFooterFiles := func(term *tuitest.Terminal, why string) {
		t.Helper()
		col, row, ok := footerFilesAt(term.Screen())
		if !ok {
			t.Fatalf("%s: the rail drew no files control:\n%s", why, term.Snapshot())
		}
		mouseClick(t, term, col+1, row, tuitest.MouseLeft, 0)
	}
	attach := func(why string) *tuitest.Terminal {
		t.Helper()
		next := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach", "e2e"}})
		if err := next.WaitFor(func(s tuitest.Screen) bool {
			_, _, ok := footerFilesAt(s)
			return ok && strings.Contains(s.Text(), sidebarHeader)
		}, bootTimeout); err != nil {
			t.Fatalf("%s: the client never attached with the rail up: %v\n%s", why, err, next.Snapshot())
		}
		return next
	}

	waitSection(term, true, "on the shipped layout")
	copiesFrame(t, term, "rail-files-switch-shown")

	clickFooterFiles(term, "hide")
	waitSection(term, false, "after a click on the files control")
	copiesFrame(t, term, "rail-files-switch-hidden")

	second := attach("second client")
	waitSection(second, false, "on a second client")
	copiesFrame(t, second, "rail-files-switch-hidden-second-client")
	waitFileHas(t, cfg, "the layout without files", uiTimeout, func(s string) bool {
		return strings.Contains(s, "sections") && strings.Contains(s, "terminals") && !strings.Contains(s, "files:")
	})

	mustCLI(t, base, "restart-server", "--yes")
	third := attach("after the restart")
	waitSection(third, false, "after a daemon restart")
	copiesFrame(t, third, "rail-files-switch-hidden-after-restart")

	clickFooterFiles(third, "show")
	waitSection(third, true, "after a second click on the files control")
	waitFileHas(t, cfg, "the layout with files", uiTimeout, func(s string) bool {
		return strings.Contains(s, "files:25")
	})

	alive(t, third, "after hiding and showing the files section")
}
