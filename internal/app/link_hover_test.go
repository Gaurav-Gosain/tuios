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
	prev := config.Links
	config.Links = config.LinksAll
	t.Cleanup(func() { config.Links = prev })

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
// Negative control: with markedLinkAt returning the cell's own column as both
// ends of the run, the span assertions fail; with the OSC 8 parse taking the
// parameters as the URL, which is the bug internal/vt/osc.go carries a comment
// about, the address assertion fails. NOT YET CONFIRMED RED.
func TestLinkHoverFindsAMarkedRun(t *testing.T) {
	const want = "https://example.com/docs"
	_, win := linkTestOS(t, "see \x1b]8;;"+want+"\x1b\\docs\x1b]8;;\x1b\\ here")

	link, ok := resolvePaneLink(win, 5, 0)
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
		if _, ok := resolvePaneLink(win, col, 0); ok {
			t.Errorf("column %d resolved to a link; the run is 4..7", col)
		}
	}
}

// TestLinkHoverFindsABareURL checks the other half: a URL a program printed as
// plain text, which is what almost every URL in a terminal is.
//
// Negative control: with the bare branch removed from resolvePaneLink, this
// fails while TestLinkHoverFindsAMarkedRun still passes, which is the split the
// appearance.links setting exists to expose.
func TestLinkHoverFindsABareURL(t *testing.T) {
	const want = "https://example.org/a"
	_, win := linkTestOS(t, "go to "+want+" now")

	link, ok := resolvePaneLink(win, 8, 0)
	if !ok {
		t.Fatal("no link resolved inside a bare URL")
	}
	if link.URL != want {
		t.Errorf("URL = %q, want %q", link.URL, want)
	}
	if link.Marked {
		t.Error("a bare URL reported itself as marked")
	}
	// "go to " is six cells, so the run starts at column 6 and covers len(want)
	// cells, all of which are single-width ASCII.
	if link.X0 != 6 || link.X1 != 6+len(want)-1 {
		t.Errorf("run = cols %d..%d, want %d..%d", link.X0, link.X1, 6, 6+len(want)-1)
	}
}

// TestLinkModeMarkedIgnoresBareURLs pins what the middle setting means. It is
// the setting for someone who wants only what a program declared, and the whole
// difference between it and "all" is this.
//
// Negative control: with the config check dropped from resolvePaneLink, the
// bare URL resolves under "marked" and this fails.
func TestLinkModeMarkedIgnoresBareURLs(t *testing.T) {
	_, win := linkTestOS(t, "go to https://example.org/a now")

	config.Links = config.LinksMarked
	if _, ok := resolvePaneLink(win, 8, 0); ok {
		t.Error("appearance.links = marked still found a bare URL")
	}
	config.Links = config.LinksOff
	if _, ok := resolvePaneLink(win, 8, 0); ok {
		t.Error("appearance.links = off still found a link")
	}
}

// TestLinkHoverYieldsToAMouseTrackingGuest is the ownership rule.
//
// A pane in terminal mode whose program asked for mouse reporting owns the
// pointer: the click handler forwards to it, so underlining a link there would
// promise an action tuios is not going to take. The suppression has to be the
// same three-part test the click path applies, or the two disagree about who
// owns the same cell.
//
// Negative control: with guestOwnsPointer returning false unconditionally, the
// last assertion fails.
func TestLinkHoverYieldsToAMouseTrackingGuest(t *testing.T) {
	m, win := linkTestOS(t, "go to https://example.org/a now")
	m.Windows[0].Workspace = 1

	sx, sy := screenOf(win, 8, 0)
	if !m.LinkHoverAt(sx, sy) {
		t.Fatal("no link under the pointer before the guest asked for the mouse")
	}

	// The guest turns on mouse reporting (DECSET 1000) and tuios is in terminal
	// mode with that pane focused: all three parts of the test hold.
	win.WriteOutput([]byte("\x1b[?1000h"))
	m.Mode = TerminalMode
	if !win.Terminal.HasMouseMode() {
		t.Fatal("the emulator did not record DECSET 1000")
	}
	if m.LinkHoverAt(sx, sy) {
		t.Error("the pointer picked up a link over a pane whose program is tracking the mouse")
	}

	// Window management mode is tuios's own, so the pane does not own the
	// pointer there even with reporting on.
	m.Mode = WindowManagementMode
	if !m.LinkHoverAt(sx, sy) {
		t.Error("window management mode handed the pointer to the guest")
	}
}

// TestLinkHoverUnderlinesTheRunOnScreen is the on-screen half. Everything above
// asserts on model state, and model state and pixels disagreeing is the failure
// this codebase keeps hitting, so this one reads the composed pane.
//
// Negative control: with the highlight branch removed from the cell loop, the
// underline never appears and this fails; with the pane left on the fast
// unfocused path, it fails for an unfocused pane only, which is the narrower
// bug the linkRun test in renderTerminal exists to stop.
func TestLinkHoverUnderlinesTheRunOnScreen(t *testing.T) {
	m, win := linkTestOS(t, "go to https://example.org/a now")

	// SGR 4 is underline, and it is what linkHoverStyle renders. Nothing in the
	// pane's own output carries it, so its presence is the highlight and its
	// absence is the lack of one.
	const underline = "\x1b[4m"

	before := m.renderTerminal(win, true, false)
	if strings.Contains(before, underline) {
		t.Fatal("the pane already draws an underline with no pointer on it")
	}

	sx, sy := screenOf(win, 8, 0)
	if !m.LinkHoverAt(sx, sy) {
		t.Fatal("no link under the pointer")
	}
	after := m.renderTerminal(win, true, false)
	if !strings.Contains(after, underline) {
		t.Fatalf("the hovered run is not underlined on screen:\n%q", after)
	}

	// And it comes off again. The pointer leaving is what marks the pane dirty,
	// so a stale cache here would leave the underline up forever.
	m.clearLinkHover()
	cleared := m.renderTerminal(win, true, false)
	if strings.Contains(cleared, underline) {
		t.Errorf("the underline outlived the pointer:\n%q", cleared)
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
	const underline = "\x1b[4m"

	sx, sy := screenOf(win, 8, 0)
	if !m.LinkHoverAt(sx, sy) {
		t.Fatal("no link under the pointer")
	}
	out := m.renderTerminal(win, false, false)
	if !strings.Contains(out, underline) {
		t.Errorf("an unfocused pane served the fast path and drew no underline:\n%q", out)
	}
}
