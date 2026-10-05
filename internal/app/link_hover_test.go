package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// linkTestOS builds one pane at the origin, writes body into it, and returns the
// model.
//
// NewDaemonWindow takes the outer rectangle and gives the emulator two cells
// less in each direction, so a 60x10 window is a 58x8 grid behind a one-cell
// border. Nothing here overrides those, because a window whose Width and
// emulator disagree is a pane that cannot exist, and the screen coordinates
// below are derived from the border rather than from a guess.
func linkTestOS(t *testing.T, body string) (*OS, *terminal.Window) {
	t.Helper()
	prev := config.Global.Links
	config.Global.Links = config.LinksAll
	t.Cleanup(func() { config.Global.Links = prev })

	win := newTestWindow(t, "aaaaaaaa1111", 60, 10)
	win.X, win.Y = 0, 0
	win.Workspace = 1
	win.WriteOutput([]byte(body))

	m := newTestOS(win)
	m.CurrentWorkspace = 1
	return m, win
}

// screenOf maps a content cell to the absolute screen cell the pane draws it
// on, through the pane's own border allowance rather than through a constant.
func screenOf(win *terminal.Window, col, row int) (int, int) {
	off := win.BorderOffset()
	return win.X + off + col, win.Y + off + row
}

// TestLinkHoverFindsAMarkedRun checks that an OSC 8 hyperlink resolves to its
// address and to exactly the cells the program marked.
//
// The expected span is counted off the literal written below, never read back
// from the resolver: "see " is four cells, the label "docs" is four more, so the
// run is columns 4 through 7 and the URL is the one in the escape.
//
// Negative control, both confirmed red: with markedLinkAt returning the cell's
// own column as both ends of the run, the span assertions fail; with the OSC 8
// parse taking the parameters as the URL, which is the bug internal/vt/osc.go
// carries a comment about, the address assertion fails.
func TestLinkHoverFindsAMarkedRun(t *testing.T) {
	const want = "https://example.com/docs"
	_, win := linkTestOS(t, "see \x1b]8;;"+want+"\x1b\\docs\x1b]8;;\x1b\\ here")

	link, ok := resolvePaneLink(win, 5, 0, &config.Global)
	if !ok {
		t.Fatal("no link resolved under a cell inside the marked run")
	}
	if link.URL != want {
		t.Errorf("URL = %q, want %q", link.URL, want)
	}
	if !link.Marked {
		t.Error("a run that came from OSC 8 did not report itself as marked")
	}
	if link.X0 != 4 || link.X1 != 7 || link.Y0 != 0 || link.Y1 != 0 {
		t.Errorf("run = rows %d..%d cols %d..%d, want row 0 cols 4..7",
			link.Y0, link.Y1, link.X0, link.X1)
	}

	// The cells on either side of the label were never marked.
	for _, col := range []int{3, 8} {
		if _, ok := resolvePaneLink(win, col, 0, &config.Global); ok {
			t.Errorf("column %d resolved to a link; the run is 4..7", col)
		}
	}
}

// TestLinkHoverMarksUnderAMouseTrackingGuest is the ownership rule.
//
// A guest that reports the mouse still loses a click on a link cell to the
// link, so marking the cell promises an action tuios takes. Every other click
// still forwards, which is what keeps the guest's own mouse UI working.
func TestLinkHoverMarksUnderAMouseTrackingGuest(t *testing.T) {
	m, win := linkTestOS(t, "go to https://example.org/a now")
	m.Windows[0].Workspace = 1

	sx, sy := screenOf(win, 8, 0)
	if !m.LinkHoverAt(sx, sy) {
		t.Fatal("no link under the pointer before the guest asked for the mouse")
	}

	// The guest turns on mouse reporting (DECSET 1000) and tuios is in terminal
	// mode with that pane focused: the pane is one whose click on a link cell
	// would be taken, so the marking stays.
	win.WriteOutput([]byte("\x1b[?1000h"))
	m.Mode = TerminalMode
	if !win.Terminal.HasMouseMode() {
		t.Fatal("the emulator did not record DECSET 1000")
	}
	if !m.LinkHoverAt(sx, sy) {
		t.Error("the pointer dropped the link over a pane whose program is tracking the mouse")
	}
}

// TestUnfocusedPaneLeavesTheFastPathToUnderline pins the one line in
// renderTerminal that a link hover has to reach past. The emulator's own
// renderer has nowhere to put an underline, so a pane on that path draws no
// highlight at all.
//
// Negative control: with hasLinkRun dropped from the fast-path condition, this
// fails and the focused-pane test above still passes, which is exactly how the
// bug would have shipped.
func TestUnfocusedPaneLeavesTheFastPathToUnderline(t *testing.T) {
	m, win := linkTestOS(t, "go to https://example.org/a now")

	sx, sy := screenOf(win, 8, 0)
	if !m.LinkHoverAt(sx, sy) {
		t.Fatal("no link under the pointer")
	}
	out := m.renderTerminal(win, false, false)
	if !isUnderlined(out) {
		t.Errorf("an unfocused pane served the fast path and drew no underline:\n%q", out)
	}
}

// TestLinkHoverFollowsASoftWrap checks that a URL the guest broke across rows
// resolves whole, from either half.
//
// The pane's grid is 58 cells wide, so the prefix below puts the URL's tail on
// the next row with nothing between the halves. Hovering the first half used to
// offer the truncated address, and hovering the second half found no link at
// all, which is what `cat README.md` looks like on any URL longer than the pane.
func TestLinkHoverFollowsASoftWrap(t *testing.T) {
	const url = "https://img.shields.io/github/commit-activity/w/Gaurav-Gosain/tuios"
	// Fill the row up to the URL so the break lands inside it.
	const lead = "src="
	_, win := linkTestOS(t, lead+url)

	grid := win.Terminal.Width()
	if len(lead)+len(url) <= grid {
		t.Fatalf("the URL fits on one row (%d cells in a %d cell grid), so this test checks nothing",
			len(lead)+len(url), grid)
	}

	// A cell in the first half, and one in the second.
	for _, probe := range []struct {
		name string
		x, y int
	}{
		{"first half", len(lead) + 4, 0},
		{"second half", 2, 1},
	} {
		link, ok := resolvePaneLink(win, probe.x, probe.y, &config.Global)
		if !ok {
			t.Errorf("%s: no link resolved", probe.name)
			continue
		}
		if link.URL != url {
			t.Errorf("%s: resolved %q, want %q", probe.name, link.URL, url)
		}
		if link.Y0 != 0 || link.X0 != len(lead) {
			t.Errorf("%s: run starts at (%d, %d), want (%d, 0)", probe.name, link.X0, link.Y0, len(lead))
		}
		if link.Y1 != 1 {
			t.Errorf("%s: run ends on row %d, want row 1", probe.name, link.Y1)
		}
		if wantX1 := len(lead) + len(url) - grid - 1; link.X1 != wantX1 {
			t.Errorf("%s: run ends at column %d, want %d", probe.name, link.X1, wantX1)
		}
	}
}

// TestLinkHoverDoesNotJoinTwoLines checks that two URLs on two lines stay two
// links, so following a wrap does not glue neighbouring output together.
//
// This is the case the one-row scan was protecting against, and the wrap rule
// has to keep protecting against it: the first line does not reach the pane's
// last column, so nothing carries onto the second.
func TestLinkHoverDoesNotJoinTwoLines(t *testing.T) {
	const first = "https://example.com/one"
	const second = "https://example.com/two"
	_, win := linkTestOS(t, first+"\r\n"+second+"\r\n")

	for _, probe := range []struct {
		y    int
		want string
	}{{0, first}, {1, second}} {
		link, ok := resolvePaneLink(win, 4, probe.y, &config.Global)
		if !ok {
			t.Errorf("row %d: no link resolved", probe.y)
			continue
		}
		if link.URL != probe.want {
			t.Errorf("row %d: resolved %q, want %q", probe.y, link.URL, probe.want)
		}
		if link.Y0 != probe.y || link.Y1 != probe.y {
			t.Errorf("row %d: the run spans rows %d to %d, want one row", probe.y, link.Y0, link.Y1)
		}
	}
}

// TestLinkHoverDoesNotJoinAFullLineToTheNext is the case a full last column
// cannot tell from a wrap: a line exactly as wide as the pane, ended with a
// newline, then a line that starts with text a URL could carry on into. The
// two are two lines, and the URL on the first must not take the second.
//
// Negative control: joining on a full last column, as this did before the
// emulator recorded wraps, resolves the URL with "tail/more" glued on.
func TestLinkHoverDoesNotJoinAFullLineToTheNext(t *testing.T) {
	const grid = 58 // the test window's content width
	head := "go https://example.com/"
	first := head + strings.Repeat("a", grid-len(head))
	_, win := linkTestOS(t, first+"\r\ntail/more\r\n")
	if w := win.Terminal.Width(); w != grid {
		t.Fatalf("the grid is %d columns, the test assumes %d", w, grid)
	}
	link, ok := resolvePaneLink(win, len(head)+2, 0, &config.Global)
	if !ok {
		t.Fatal("no link resolved")
	}
	if want := first[3:]; link.URL != want {
		t.Errorf("resolved %q, want %q", link.URL, want)
	}
	if link.Y1 != 0 {
		t.Errorf("the run ends on row %d, want row 0", link.Y1)
	}
}
