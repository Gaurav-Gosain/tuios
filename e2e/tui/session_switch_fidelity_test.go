package tuie2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// What a session switch does to a pane that is not black and white.
//
// The in-process matrix compares two emulators. This drives a real client in a
// real terminal and reads the pixels it actually painted, which is where the
// report came from: colours changing and cells going missing after a switch.
//
// tuitest keeps a cell's colour encoding, so a palette entry and the RGB it
// resolves to are two different values here. That distinction is the whole of
// the colour bug: a palette entry follows the user's terminal theme and the RGB
// does not, so a pane that comes back in RGB is a pane repainted in shades the
// user never chose, even though it is the same shade on the developer's own
// default theme.

// twoSessionClientIn is twoSessionClient with extra flags on the client, for
// the tests whose bug only shows under a particular border setting.
func twoSessionClientIn(t *testing.T, flags ...string) *tuitest.Terminal {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)

	for _, name := range []string{"alpha", "bravo"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v: %s", name, err, out)
		}
	}

	term := startIn(t, base, startOpts{
		cols: 120, rows: 40,
		args: append([]string{"attach", "alpha"}, flags...),
	})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached to alpha: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	enterTerminalMode(t, term)
	return term
}

// paintedLine is the row a marker is on, described cell by cell.
func paintedLine(t *testing.T, term *tuitest.Terminal, marker string) []string {
	t.Helper()
	s := term.Screen()
	cols, rows := s.Size()
	for row := range rows {
		if !strings.Contains(s.Line(row), marker) {
			continue
		}
		out := make([]string, 0, cols)
		for col := range cols {
			c := s.Cell(col, row)
			if c.Rune == ' ' || c.Rune == 0 {
				continue
			}
			out = append(out, fmt.Sprintf("%q fg=%v bg=%v b=%t i=%t u=%t r=%t s=%t",
				c.Rune, c.Fg, c.Bg, c.Bold, c.Italic, c.Underline, c.Reverse, c.Strikethrough))
		}
		return out
	}
	t.Fatalf("no row holds %q:\n%s", marker, term.Snapshot())
	return nil
}

func diffPainted(t *testing.T, what string, before, after []string) {
	t.Helper()
	if len(before) != len(after) {
		t.Errorf("%s: %d painted cells before the switch, %d after:\n  before %v\n  after  %v",
			what, len(before), len(after), before, after)
		return
	}
	var diffs []string
	for i := range before {
		if before[i] != after[i] {
			diffs = append(diffs, fmt.Sprintf("  cell %d\n    before %s\n    after  %s", i, before[i], after[i]))
		}
	}
	if len(diffs) > 0 {
		if len(diffs) > 6 {
			diffs = append(diffs[:6], fmt.Sprintf("  ... and %d more", len(diffs)-6))
		}
		t.Errorf("%s: %d of %d cells came back painted differently:\n%s",
			what, len(diffs), len(before), strings.Join(diffs, "\n"))
	}
}

// TestSessionSwitchKeepsPaneColours is the report: leave a coloured pane for
// another session, come back, and every cell must be painted as it was.
func TestSessionSwitchKeepsPaneColours(t *testing.T) {
	const marker = "COLMARK-7781"

	term := twoSessionClient(t)
	// The palette, an indexed colour, a truecolour and the attributes, all on
	// one line so one comparison covers them.
	// The marker is computed by the shell so the echoed command line cannot
	// match it and be mistaken for the pane's output.
	runInShell(t, term,
		`printf '\033[31mR\033[32mG\033[34mB\033[91mBR\033[m\033[38;5;208mIDX\033[38;2;10;150;200mTRU\033[m\033[1mBOLD\033[m\033[4mUND\033[m\033[7mREV\033[m %s\n' "$(echo COLMARK)-7781"`,
		marker, shellTimeout)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled: %v", err)
	}
	before := paintedLine(t, term, marker)

	switchSession(t, term, "bravo")
	switchSession(t, term, "alpha")
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled after the switches: %v", err)
	}

	diffPainted(t, "the marker line", before, paintedLine(t, term, marker))
}

// TestSessionSwitchKeepsThePen covers the other half: a guest that set a colour
// and did not reset it. What is already on screen is restored from the
// snapshot, but the next line the pane prints is painted by the client's own
// emulator, from whatever rendition it was left holding.
func TestSessionSwitchKeepsThePen(t *testing.T) {
	const first = "PENMARK-A-4419"
	const second = "PENMARK-B-4419"

	term := twoSessionClient(t)
	// The colour is left in force: no reset follows it.
	runInShell(t, term, `printf '\033[35;1m%s\n' "$(echo PENMARK)-A-4419"`, first, shellTimeout)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled: %v", err)
	}
	want := paintedLine(t, term, first)

	switchSession(t, term, "bravo")
	switchSession(t, term, "alpha")

	// Printed after the switch, so it is painted by the pen the client came
	// back holding rather than restored from the snapshot.
	runInShell(t, term, `printf '%s\n' "$(echo PENMARK)-B-4419"`, second, shellTimeout)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled: %v", err)
	}
	got := paintedLine(t, term, second)

	// Compare how they are painted, not what they say: the two markers differ
	// by one letter and are otherwise the same length.
	styleOnly := func(cells []string) []string {
		out := make([]string, len(cells))
		for i, c := range cells {
			if _, style, ok := strings.Cut(c, " "); ok {
				out[i] = style
			}
		}
		return out
	}
	diffPainted(t, "the line printed after the switch", styleOnly(want), styleOnly(got))
}

// TestSessionSwitchKeepsTheBottomOfAFullScreenTUI is the reported shape: nvim
// open in a pane, switch away, switch back, and the bottom two rows of the
// editor are blank. The last line of the file and the whole status line are
// gone, everything above them is untouched, and the pane is still the same
// height, so the rows were not pushed off: they were emptied.
//
// The guest asks the terminal how tall it is and then writes to every row it
// was told it has, so the marker on the last row is on the last row the pane
// actually has, whatever the dock and the borders leave it.
func TestSessionSwitchKeepsTheBottomOfAFullScreenTUI(t *testing.T) {
	// Shared borders is what the pane this was reported on was running under,
	// and it is load-bearing: it makes the tiler mark the pane Tiled, which
	// takes the border allowance out of the content height. The window's
	// emulator is built with that allowance still subtracted, so the snapshot
	// is two rows taller than the grid it is blitted into.
	term := twoSessionClientIn(t, "--shared-borders")
	windowManagementMode(t, term)
	enableTiling(t, term)
	enterTerminalMode(t, term)

	// A full-screen program that fills its whole height and puts something
	// recognisable on the last two rows, which is where nvim keeps the last
	// line of the file and its status line.
	runInShell(t, term,
		`H=$(stty size | cut -d' ' -f1); printf '\033[?1049h\033[H\033[2J'; `+
			`i=1; while [ $i -le $H ]; do printf '\033[%d;1HFILLROW-%d' $i $i; i=$((i+1)); done; `+
			`printf '\033[%d;1HLASTROW-9902\033[%d;1HNEXTTOLAST-9902' $H $((H-1))`,
		"LASTROW-9902", shellTimeout)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the alternate screen never settled: %v", err)
	}
	before := term.Screen().Text()
	if !strings.Contains(before, "NEXTTOLAST-9902") {
		t.Fatalf("the guest never drew its second-to-last row, so the case is not set up:\n%s", term.Snapshot())
	}

	switchSession(t, term, "bravo")
	switchSession(t, term, "alpha")
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled after the switches: %v", err)
	}

	after := term.Screen().Text()
	for _, want := range []string{"LASTROW-9902", "NEXTTOLAST-9902"} {
		if !strings.Contains(after, want) {
			t.Errorf("the bottom of the full-screen program came back blank: %q is gone after the round trip\n--- before ---\n%s\n--- after ---\n%s",
				want, before, after)
		}
	}
}

// TestSessionSwitchKeepsTheScreenUnderAltScreen is the cells-going-missing half.
// A full-screen program is running when the pane is left; quitting it after the
// switch has to reveal the shell's screen, which the client is only holding if
// the snapshot carried it.
func TestSessionSwitchKeepsTheScreenUnderAltScreen(t *testing.T) {
	const shellMark = "UNDERMARK-3307"

	term := twoSessionClient(t)
	runInShell(t, term, `printf '%s\n' "$(echo UNDERMARK)-3307"`, shellMark, shellTimeout)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled: %v", err)
	}
	before := term.Screen().Text()

	// Enter the alternate screen and draw in it, as vim or htop would. Written
	// by hand so the test does not depend on an editor being installed.
	runInShell(t, term, `printf '\033[?1049h\033[H\033[2J%s\n' "$(echo ALTBODY)-3307"`, "ALTBODY-3307", shellTimeout)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the alternate screen never settled: %v", err)
	}
	if strings.Contains(term.Screen().Text(), shellMark) {
		t.Fatalf("the pane never entered the alternate screen, so the case is not set up:\n%s", term.Snapshot())
	}

	switchSession(t, term, "bravo")
	switchSession(t, term, "alpha")

	// The guest quits, which puts the shell's screen back on display.
	runInShell(t, term, `printf '\033[?1049l'`, shellMark, shellTimeout)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled after leaving the alternate screen: %v", err)
	}
	if after := term.Screen().Text(); !strings.Contains(after, shellMark) {
		t.Errorf("quitting the full-screen program left the pane without the shell's screen underneath it\n  before %q\n  after  %q\n%s",
			before, after, term.Snapshot())
	}
}
