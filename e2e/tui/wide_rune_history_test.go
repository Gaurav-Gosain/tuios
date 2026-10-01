package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"github.com/charmbracelet/x/ansi"
)

// A double-width rune in a pane's history, and a pane that narrows until the
// rune sits across its last column.
//
// Found by mirroring tuios in Collie (v1.15.0): a pane printed an emoji, a
// client attached at another size, and the emoji was a blank in every capture
// from then on, Collie's mirror included. The resize used to blank the rune in
// the stored history line so that the row could not be drawn wider than the
// pane. That lost the character for good, because history keeps the width it
// was written at and the pane can widen again.
//
// The rune now stays in the history. So this test asserts three things in one
// run:
//
//   - while the pane is narrow and scrolled back to the line, the client's
//     frame stops the row at the pane's border. That is the reason the
//     blanking existed, and it has to stay true without it.
//   - while the pane is narrow, a text screenshot of the pane with its history
//     has no row wider than the pane. The screenshot grid clips the row with
//     vt.ClipHistoryRow.
//   - once the pane is wide again, capture-pane gives back every rune.
//
// Two lines are printed, one with its runes on even columns and one on odd
// columns, so one of them straddles the edge whatever width the layout gives
// the pane.
//
// NEGATIVE CONTROLS, both recorded in NEGATIVE_CONTROLS.md:
//
//   - put back the call in vt.Emulator.Resize that blanked the straddling cell
//     of every history line: the capture after widening has 29 runes on one of
//     the lines.
//   - drop the vt.ClipHistoryRow call in the screenshot grid: the screenshot
//     row is one column wider than the pane.
func TestWideRuneInHistorySurvivesANarrowPane(t *testing.T) {
	const (
		session = "wide-hist"
		runes   = 30
		rune2   = "世"
	)
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	w := firstWindow(t, base, session)

	// The width the detached pane has. Both lines have to fit it, or they wrap
	// when printed and no rune ever sits at a column a resize can cut.
	wide := paneCols(t, base, session, w.ID, "WIDE0", func(int) bool { return true })
	lineA := "WRA" + strings.Repeat(rune2, runes)  // runes on odd columns
	lineB := "WRBx" + strings.Repeat(rune2, runes) // runes on even columns
	if wide < len("WRBx")+2*runes {
		t.Fatalf("the detached pane is %d columns, too narrow for a %d-column line", wide, len("WRBx")+2*runes)
	}
	// paneEmitCmd spells every byte in octal, so the echoed command line holds
	// neither marker and only the printed lines can match.
	if err := paneSend(base, session, w.ID, paneEmitCmd(lineA+"\n"+lineB+"\n")); err != nil {
		t.Fatalf("print the lines: %v", err)
	}
	// Push both lines off the screen and into history.
	if err := paneSend(base, session, w.ID, "seq 1 60\n"); err != nil {
		t.Fatalf("fill the screen: %v", err)
	}

	// A client that then shrinks to 50 columns narrows the pane below the 64
	// the lines need. It attaches wider first: the mode banner the attach
	// waits for does not fit a 50-column dock.
	term := attachIn(t, base, session, startOpts{cols: 100, rows: 20})
	if err := term.Resize(50, 20); err != nil {
		t.Fatalf("narrow the client: %v", err)
	}
	narrow := paneCols(t, base, session, w.ID, "NARROW", func(c int) bool { return c < len(lineA) })
	t.Logf("the pane is %d columns wide, then %d", wide, narrow)

	// The screenshot of the narrow pane, history included.
	shotFile := filepath.Join(t.TempDir(), "narrow.txt")
	if out, err := tuiosOut(base, "screenshot", "-s", session, "-w", w.ID, "--scrollback",
		"--format", "txt", "--frame", "none", "--no-copy", "--out", shotFile); err != nil {
		t.Fatalf("screenshot: %v: %s", err, out)
	}
	shotText, err := os.ReadFile(shotFile)
	if err != nil {
		t.Fatalf("read the screenshot: %v", err)
	}
	shotRows := 0
	for _, l := range strings.Split(string(shotText), "\n") {
		if !strings.HasPrefix(l, "WRA") && !strings.HasPrefix(l, "WRBx") {
			continue
		}
		shotRows++
		if got := ansi.StringWidth(l); got > narrow {
			t.Fatalf("a screenshot row is %d columns wide in a %d-column pane: %q", got, narrow, l)
		}
	}
	if shotRows != 2 {
		t.Fatalf("the screenshot holds %d of the 2 lines:\n%s", shotRows, shotText)
	}

	// Scroll the pane back until the lines are on screen.
	col, row := paneCell(t, term)
	deadline := time.Now().Add(uiTimeout)
	for !screenHas(term.Screen(), "WRA", "WRBx") {
		if time.Now().After(deadline) {
			t.Fatalf("scrolling back never showed the two lines\n%s", term.Snapshot())
		}
		wheelAt(t, term, col, row, tuitest.MouseWheelUp, 3)
		time.Sleep(150 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	s := term.Screen()
	borders := borderColumns(s)
	if len(borders) < 2 {
		t.Fatalf("found border columns %v, want the pane's left and right ones\n%s", borders, term.Snapshot())
	}
	right := borders[len(borders)-1]
	_, rows := s.Size()
	checked := 0
	for r := range rows {
		line := s.Line(r)
		if !strings.Contains(line, "WRA") && !strings.Contains(line, "WRBx") {
			continue
		}
		checked++
		t.Logf("row %d: %q", r, line)
		// The positive half: the line really reaches the border, so the
		// question of what is drawn at the edge is a real one on this row.
		last := -1
		for c := range right {
			if s.Cell(c, r).Content == rune2 {
				last = c
			}
		}
		if last < right-5 {
			t.Fatalf("row %d stops at column %d, short of the pane's border at column %d, so it cannot test the edge: %q\n%s",
				r, last, right, line, term.Snapshot())
		}
		if c := s.Cell(right, r); c.Content != "│" {
			t.Fatalf("row %d covers the pane's right border at column %d with %q (width %d): "+
				"a history row was drawn wider than the pane\n%s", r, right, c.Content, c.Width, term.Snapshot())
		}
	}
	if checked != 2 {
		t.Fatalf("checked %d rows holding the lines, want 2\n%s", checked, term.Snapshot())
	}

	// Widen the pane again. Every rune has to be in the history still.
	if err := term.Resize(160, 40); err != nil {
		t.Fatalf("resize the client: %v", err)
	}
	again := paneCols(t, base, session, w.ID, "WIDE1", func(c int) bool { return c >= len(lineB) })
	t.Logf("and then %d columns", again)

	hist, err := daemonScrollback(base, session, w.ID, 200)
	if err != nil {
		t.Fatalf("capture-pane: %v", err)
	}
	found := 0
	for _, l := range hist {
		l = strings.TrimRight(l, " ")
		if strings.HasPrefix(l, "WRA") || strings.HasPrefix(l, "WRBx") {
			found++
			if n := strings.Count(l, rune2); n != runes {
				t.Errorf("after the pane narrowed to %d columns and widened to %d, the history line "+
					"%q holds %d of its %d runes", narrow, again, l, n, runes)
			}
		}
	}
	if found != 2 {
		t.Fatalf("found %d of the 2 lines in the history:\n%s", found, strings.Join(hist, "\n"))
	}
}

// paneCols asks the pane's own shell how many columns its terminal has, which
// is the size of the daemon's emulator for that pane, and waits until ok holds.
// tag keeps one question's answer apart from an earlier one on the screen.
func paneCols(t *testing.T, base, session, window, tag string, ok func(int) bool) int {
	t.Helper()
	// The tag is split in the command so only the printed answer matches.
	half := len(tag) / 2
	deadline := time.Now().Add(shellTimeout)
	for {
		cmd := fmt.Sprintf("echo %s\"\"%s $(stty size)\n", tag[:half], tag[half:])
		if err := paneSend(base, session, window, cmd); err != nil {
			t.Fatalf("ask the pane its size: %v", err)
		}
		time.Sleep(300 * time.Millisecond)
		grid, err := daemonPane(base, session, window)
		if err == nil {
			for i := len(grid) - 1; i >= 0; i-- {
				f := strings.Fields(grid[i])
				if len(f) == 3 && f[0] == tag {
					if c, err := strconv.Atoi(f[2]); err == nil && ok(c) {
						return c
					}
					break
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane never reported a size that fits (%s)\n%s", tag, strings.Join(grid, "\n"))
		}
	}
}
