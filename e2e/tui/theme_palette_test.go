package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// probeCmd prints an SGR 31 marker the shell assembles itself, so the command
// line the harness typed never carries the marker and every hit is real output.
func probeCmd(tag string) string {
	return `printf '\033[31m%s` + tag + `\033[0m\n' "$(echo INK)"`
}

// probeInk returns the colour the host terminal was given for the first cell of
// marker, which is the only place the question can honestly be asked: what
// tuios put on the wire, read off the terminal it wrote to.
func probeInk(t *testing.T, term *tuitest.Terminal, marker string) tuitest.Color {
	t.Helper()
	if err := term.WaitForText(marker, uiTimeout); err != nil {
		t.Fatalf("the probe never printed %q: %v\n%s", marker, err, term.Snapshot())
	}
	s := term.Screen()
	_, rows := s.Size()
	for row := rows - 1; row >= 0; row-- {
		if col := strings.Index(s.Line(row), marker); col >= 0 {
			return s.Cell(col, row).Fg
		}
	}
	t.Fatalf("no row carried %q on its own\n%s", marker, term.Snapshot())
	return tuitest.Color{}
}

// pickTheme drives the theme picker to a name and waits for it to close.
func pickTheme(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	if err := term.SendKeys(tuitest.Ctrl('p')); err != nil {
		t.Fatalf("open palette: %v", err)
	}
	if err := term.WaitForText(paletteTitle, uiTimeout); err != nil {
		t.Fatalf("palette did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("Theme Picker"); err != nil {
		t.Fatalf("type palette query: %v", err)
	}
	if err := term.WaitForText("Theme Picker", uiTimeout); err != nil {
		t.Fatalf("palette never filtered to the theme picker: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("open the theme picker: %v", err)
	}
	if err := term.WaitForText("themes", uiTimeout); err != nil {
		t.Fatalf("the theme picker did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(name); err != nil {
		t.Fatalf("type the theme name: %v", err)
	}
	if err := term.WaitForText(name, uiTimeout); err != nil {
		t.Fatalf("the picker never filtered to %q: %v\n%s", name, err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("apply the theme: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "themes")
	}, uiTimeout); err != nil {
		t.Fatalf("the theme picker did not close: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)
}

// TestThemeOffLeavesThePaletteToTheTerminal is the on-screen form of the rule.
// A palette index belongs to the user's terminal, so with no theme loaded an
// SGR 31 in a pane has to reach the host as an index and be painted from the
// user's own red. Applying a theme is what replaces it, and removing that theme
// has to give it back.
//
// Before the palette was split into a theme layer and the guest's own, turning
// a theme off left all sixteen of its colours in the emulator: every pane went
// on painting in a theme the user had just removed, for the life of the
// session.
func TestThemeOffLeavesThePaletteToTheTerminal(t *testing.T) {
	term, _ := start(t, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	runInShell(t, term, probeCmd("A"), "INKA", shellTimeout)
	if got := probeInk(t, term, "INKA"); got.Kind != tuitest.ColorIndexed || got.Index != 1 {
		t.Errorf("with no theme, SGR 31 reached the host as %+v, want palette index 1", got)
	}

	leaveTerminalMode(t, term)
	pickTheme(t, term, "catppuccin_mocha")
	enterTerminalMode(t, term)

	// A theme replaces the index with its own colour. The harness terminal is
	// not truecolor, so that colour arrives stepped down to a 256 slot; what
	// makes the point either way is that slot 1, the user's own red, is no
	// longer what the host was asked for.
	runInShell(t, term, probeCmd("B"), "INKB", shellTimeout)
	if got := probeInk(t, term, "INKB"); got.Kind == tuitest.ColorIndexed && got.Index == 1 {
		t.Errorf("with a theme, SGR 31 still reached the host as %+v, want the theme's own colour", got)
	}

	leaveTerminalMode(t, term)
	pickTheme(t, term, "none")
	enterTerminalMode(t, term)

	runInShell(t, term, probeCmd("C"), "INKC", shellTimeout)
	if got := probeInk(t, term, "INKC"); got.Kind != tuitest.ColorIndexed || got.Index != 1 {
		t.Errorf("after the theme was removed, SGR 31 reached the host as %+v, want palette index 1 back", got)
	}
}
