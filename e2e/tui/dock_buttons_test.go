package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// dockButtonsConfig puts the button row on the right of the dock, beside the
// clock and the session controls, as the Simple profile does.
const dockButtonsConfig = `
[startup]
tiled = true

[dock]
right = ["buttons", "clock", "session-controls"]
`

// dockRow is the screen row that holds want, searched from the bottom, where
// the suite's dock sits. It fails the test when no row holds it.
func dockRowWith(t *testing.T, term *tuitest.Terminal, want string) (row int, line string) {
	t.Helper()
	s := term.Screen()
	_, rows := s.Size()
	for r := rows - 1; r >= max(0, rows-3); r-- {
		if l := s.Line(r); strings.Contains(l, want) {
			return r, l
		}
	}
	t.Fatalf("no dock row holds %q\n%s", want, term.Snapshot())
	return 0, ""
}

// clickDockWord clicks the middle of a word on the dock row.
func clickDockWord(t *testing.T, term *tuitest.Terminal, word string) {
	t.Helper()
	row, line := dockRowWith(t, term, word)
	col := len([]rune(line[:strings.Index(line, word)])) + len(word)/2
	leftClick(t, term, col, row)
}

// TestDockButtonsRunTheirActions is the button row on the real binary: the
// five words are on the bar, New opens a terminal, Split splits the pane,
// and the pointer on a button names what it does.
func TestDockButtonsRunTheirActions(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, dockButtonsConfig)
	term := startIn(t, base, startOpts{})
	waitBoot(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "New", "Split", "Close", "Help", "Settings")
	}, uiTimeout); err != nil {
		t.Fatalf("the button row never drew its five words: %v\n%s", err, term.Snapshot())
	}

	clickDockWord(t, term, "New")
	waitWindowCount(t, term, 1, "the New button")
	clickDockWord(t, term, "Split")
	waitWindowCount(t, term, 2, "the Split button")

	row, line := dockRowWith(t, term, "Close")
	moveMouse(t, term, len([]rune(line[:strings.Index(line, "Close")]))+2, row)
	if err := term.WaitForText("Close this pane", uiTimeout); err != nil {
		t.Fatalf("the Close button never showed its label: %v\n%s", err, term.Snapshot())
	}
	dockButtonsShot(t, term, "dock-buttons-120")
}

// TestDockButtonsLeaveASignWhenNarrow is the narrow dock. The words give way
// to icons, then buttons fold into a more button, and the more button lists
// the buttons that did not fit. No button goes without a sign.
func TestDockButtonsLeaveASignWhenNarrow(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, dockButtonsConfig)

	// At 80 columns every button is on the bar, as a word or as an icon.
	wide := startIn(t, base, startOpts{cols: 80, rows: 24, args: []string{"--ascii-only"}})
	waitBoot(t, wide)
	newWindow(t, wide)
	waitWindowCount(t, wide, 1, "a shell at 80 columns")
	if err := wide.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled: %v\n%s", err, wide.Snapshot())
	}
	_, line := dockRowWith(t, wide, "1:")
	words := strings.Contains(line, "Settings")
	icons := strings.Contains(line, " +   |   x   ?   * ")
	if !words && !icons {
		t.Fatalf("at 80 columns a button left the bar with no sign\n%s", wide.Snapshot())
	}
	dockButtonsShot(t, wide, "dock-buttons-80")

	// At 44 columns some buttons fold into the more button.
	narrow := startIn(t, base, startOpts{cols: 44, rows: 24, args: []string{"--ascii-only"}})
	waitBoot(t, narrow)
	newWindow(t, narrow)
	waitWindowCount(t, narrow, 1, "a shell at 44 columns")
	if err := narrow.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled: %v\n%s", err, narrow.Snapshot())
	}
	row, line := dockRowWith(t, narrow, "1:")
	more := strings.LastIndex(line, " > ")
	if more < 0 {
		t.Fatalf("at 44 columns the more button is missing\n%s", narrow.Snapshot())
	}
	dockButtonsShot(t, narrow, "dock-buttons-44")
	leftClick(t, narrow, len([]rune(line[:more]))+1, row)
	waitMenu(t, narrow, "the more button", "More", "Settings")
	dockButtonsShot(t, narrow, "dock-buttons-44-more")
}

// dockButtonsShot saves the frame as text and as a PNG drawn by tuios.dev/shot.
func dockButtonsShot(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	saveFrame(t, term, name)
	savePNG(t, term.Screen(), shot.XTermPalette(), artifactDir(t), name)
}
