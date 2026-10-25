package tuie2e

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// railPill is the dock mode pill's text while the sidebar rail owns the
// keyboard. Asserting on it proves the keyboard scope actually took, not just
// that a key was sent.
const railPill = "SIDEBAR"

// TestSidebarKeyboardSwitchAndExit drives the rail entirely by keyboard against
// a real daemon: s enters the rail (the dock pill reads SIDEBAR), j walks the
// cursor down to the other session, enter switches to it, and esc leaves the
// rail so focus returns to the panes. It mirrors the mouse switch test with keys.
func TestSidebarKeyboardSwitchAndExit(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "alpha", "--detach"); err != nil {
		t.Fatalf("create alpha: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new", "bravo", "--detach"); err != nil {
		t.Fatalf("create bravo: %v: %s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "alpha"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("normalise to window mode: %v", err)
	}
	if err := term.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("client never settled in window management mode: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)

	toggleSidebarViaPalette(t, term)
	waitForAll(t, term, uiTimeout, "sidebar with both session rows", sidebarHeader, "bravo")

	// s enters the rail: the dock pill switches to SIDEBAR, the authoritative
	// "who owns the keyboard" indicator.
	if err := term.SendKeys("s"); err != nil {
		t.Fatalf("enter rail: %v", err)
	}
	if err := term.WaitForText(railPill, uiTimeout); err != nil {
		t.Fatalf("s did not give the keyboard to the rail: %v\n%s", err, term.Snapshot())
	}

	// Entering the rail lands the cursor on the attached session, so one j is
	// the row below it: bravo, the next session row. The sessions header's add
	// control takes the nav slot above the first session row, not between two of
	// them, so it does not stand in this step's way.
	if err := term.SendKeys("j"); err != nil {
		t.Fatalf("move cursor: %v", err)
	}

	// enter activates the cursor row and switches to bravo.
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("activate cursor row: %v", err)
	}
	if err := term.WaitForText("Session: bravo", uiTimeout); err != nil {
		t.Fatalf("enter on the bravo row did not switch sessions: %v\n%s", err, term.Snapshot())
	}

	// esc leaves the rail: focus returns to the panes and the SIDEBAR pill goes.
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("leave rail: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), railPill)
	}, uiTimeout); err != nil {
		t.Fatalf("esc did not return focus to the panes: %v\n%s", err, term.Snapshot())
	}

	alive(t, term, "after driving the sidebar by keyboard")
}

// railBandRow is the rail row the keyboard cursor stands on: the one row
// whose content cells carry a ground or reverse video while the rail paints
// no ground of its own. It returns -1 when no row does, as while the cursor
// is on a header's add control.
func railBandRow(s tuitest.Screen) int {
	_, rows := s.Size()
	for r := range rows {
		for x := 1; x < railWidth-1; x++ {
			c := s.Cell(x, r)
			if c.Bg.Kind != tuitest.ColorDefault || c.Reverse {
				return r
			}
		}
	}
	return -1
}

// railRowMarkedOffGround reports whether rail row r carries a mark that is
// not a ground: a text cell past the gutter in underline or reverse video.
func railRowMarkedOffGround(s tuitest.Screen, r int) bool {
	for x := 1; x < railWidth-1; x++ {
		c := s.Cell(x, r)
		if strings.TrimSpace(c.Content) != "" && (c.Underline || c.Reverse) {
			return true
		}
	}
	return false
}

// railRowGroundIs reports whether rail row r stands on want: every cell
// between the gutter and the edge rule carries it as its background.
func railRowGroundIs(s tuitest.Screen, r int, want tuitest.Color) bool {
	for x := 1; x < railWidth-1; x++ {
		if s.Cell(x, r).Bg != want {
			return false
		}
	}
	return true
}

// railHasGround reports whether any cell of the rail carries want as its
// background.
func railHasGround(s tuitest.Screen, want tuitest.Color) bool {
	_, rows := s.Size()
	for r := range rows {
		for x := range railWidth {
			if s.Cell(x, r).Bg == want {
				return true
			}
		}
	}
	return false
}

// TestRailCursorShowsWithoutItsGround (discussion 1094): with the rail's
// background off, the keyboard cursor row has to differ from its neighbours
// by more than the ground, which a see-through terminal can show as almost
// nothing. The cursor walks down to the first pane row with j, then to the
// next with j and back with k. Each step requires the mark on the cursor row
// and on no other rail row. It runs on a dark terminal at truecolor and at 16
// colours, and under a light theme at truecolor, and saves a PNG of each.
//
// Two more cases set appearance.sidebar.cursor_background (also discussion
// 1094). At truecolor the cursor row must stand on that colour from the
// gutter to the edge rule, and after the config file is rewritten with
// another colour the row must take the new one with no restart. At 16
// colours the option must paint nothing: the cursor stays reverse video, and
// no rail cell carries the colour.
//
// How this could pass wrongly, written down first:
//   - The cursor row could be found by its text, and the test would then
//     read a row with no cursor. It is found by its ground instead, the
//     one thing main draws for it.
//   - A mark on every row would pass a test that reads one row, so every
//     other rail row must carry no mark.
//   - The rail could paint a ground of its own, which hides the bug. The
//     config sets the rail background off.
//   - The option could paint every rail row, or only the text cells. The
//     ground is read on every cell of the cursor row, and railBandRow still
//     has to find exactly that row.
//
// Negative control: on main the truecolor cursor row differs only by its
// ground, and the first wait fails in the dark and light truecolor cases.
func TestRailCursorShowsWithoutItsGround(t *testing.T) {
	magenta := tuitest.Color{Kind: tuitest.ColorRGB, R: 0xff, G: 0x00, B: 0xff}
	cyan := tuitest.Color{Kind: tuitest.ColorRGB, R: 0x00, G: 0xc0, B: 0xc0}
	cases := []struct {
		name, theme string
		depth       chromeDepth
		cursorBg    bool // set cursor_background to magenta
	}{
		{"dark-truecolor", "", chromeDepths[2], false},
		{"dark-16", "", chromeDepths[0], false},
		{"light-truecolor", "catppuccin_latte", chromeDepths[2], false},
		{"cursor-bg-truecolor", "", chromeDepths[2], true},
		{"cursor-bg-16", "", chromeDepths[0], true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := "[appearance]\nbackground = \"off\"\n"
			if tc.theme != "" {
				cfg += "theme = \"" + tc.theme + "\"\n"
			}
			cfg += "\n[appearance.sidebar]\nenabled = true\nwidth = " + strconv.Itoa(railWidth) + "\nbackground = \"off\"\n"
			if tc.cursorBg {
				cfg += "cursor_background = \"#ff00ff\"\n"
			}
			term, base := railClient(t, "e2e", cfg, startOpts{cols: 120, rows: 30, env: tc.depth.env})
			renameWindow(t, term, "P1")
			newWindow(t, term)
			renameWindow(t, term, "P2")
			if err := term.SendKeys("s"); err != nil {
				t.Fatalf("focus the rail: %v", err)
			}
			if err := term.WaitForText(railPill, uiTimeout); err != nil {
				t.Fatalf("s did not give the keyboard to the rail: %v\n%s", err, term.Snapshot())
			}
			dir := artifactDir(t)
			host := hostPalette(t, tc.theme)
			// on reports whether the cursor stands on the row of name and that
			// row alone carries the mark.
			on := func(name string) func(tuitest.Screen) bool {
				return func(s tuitest.Screen) bool {
					r := railRowOf(s, name)
					if r < 0 || railBandRow(s) != r {
						return false
					}
					_, rows := s.Size()
					for y := range rows {
						// Only the rail's own lines, which end in its edge
						// rule. The dock below has underlined cells of its
						// own in the same columns.
						if edge := s.Cell(railWidth-1, y).Content; edge == " " || edge == "" || edge == "─" {
							continue
						}
						if railRowMarkedOffGround(s, y) != (y == r) {
							return false
						}
					}
					return true
				}
			}
			// Walk down to P1's row. The headers' add controls sit between
			// the rows, so the number of steps is read off the screen.
			for range 6 {
				if r := railRowOf(term.Screen(), "P1"); r >= 0 && railBandRow(term.Screen()) == r {
					break
				}
				if err := term.SendKeys("j"); err != nil {
					t.Fatalf("move the cursor: %v", err)
				}
				time.Sleep(insertGuard)
			}
			err := term.WaitFor(on("P1"), uiTimeout)
			savePNG(t, term.Screen(), host, dir, "cursor-"+tc.name+"-p1")
			saveArtifact(t, term, dir, "cursor-"+tc.name+"-p1")
			if err != nil {
				t.Fatalf("the cursor row on P1 differs from its neighbours only by its ground: %v\n%s", err, term.SnapshotStyled())
			}
			if tc.cursorBg {
				scr := term.Screen()
				r := railRowOf(scr, "P1")
				truecolor := tc.depth.name == "truecolor"
				if truecolor && !railRowGroundIs(scr, r, magenta) {
					t.Fatalf("cursor_background is set, but the cursor row is not on #ff00ff:\n%s", term.SnapshotStyled())
				}
				if !truecolor && railHasGround(scr, magenta) {
					t.Fatalf("cursor_background painted the rail at 16 colours:\n%s", term.SnapshotStyled())
				}
				if truecolor {
					// Hot reload: a new colour in the file reaches the row
					// with no restart. The file is read back rather than
					// rebuilt from cfg, since the harness pins its looks in it.
					body := readFileString(t, configPathIn(base))
					saveConfigLikeAnEditor(t, base, strings.Replace(body, "#ff00ff", "#00c0c0", 1))
					err := term.WaitFor(func(s tuitest.Screen) bool {
						r := railRowOf(s, "P1")
						return r >= 0 && railBandRow(s) == r && railRowGroundIs(s, r, cyan)
					}, configWatchTimeout)
					savePNG(t, term.Screen(), host, dir, "cursor-"+tc.name+"-reload")
					if err != nil {
						t.Fatalf("the cursor row did not take the reloaded cursor_background: %v\n%s", err, term.SnapshotStyled())
					}
				}
			}
			if err := term.SendKeys("j"); err != nil {
				t.Fatalf("move the cursor down: %v", err)
			}
			if err := term.WaitFor(on("P2"), uiTimeout); err != nil {
				t.Fatalf("j did not move the cursor mark to P2: %v\n%s", err, term.SnapshotStyled())
			}
			savePNG(t, term.Screen(), host, dir, "cursor-"+tc.name+"-p2")
			if err := term.SendKeys("k"); err != nil {
				t.Fatalf("move the cursor up: %v", err)
			}
			if err := term.WaitFor(on("P1"), uiTimeout); err != nil {
				t.Fatalf("k did not move the cursor mark back to P1: %v\n%s", err, term.SnapshotStyled())
			}
		})
	}
}
