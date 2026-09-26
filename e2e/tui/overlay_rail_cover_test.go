package tuie2e

import (
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestWhichKeySitsBesideTheRail presses the prefix at 120 columns with the
// shipped looks. The which-key overlay is anchored to the bottom-right corner,
// and it took the screen's corner, not the panes': it sat over the rail with
// the rail's last two columns showing past its edge, a "+" and the "d" of
// "cd" beside the bindings. It fits beside the rail at this width, so it now
// goes there and the rail stays whole. The frame is saved under artifactDir.
//
// How this could pass wrongly, written down first:
//   - The overlay could not be open yet, so it is waited for by a binding
//     only it lists.
//   - The rail could be drawn over and its header happen to be above the
//     overlay's top, so every row of the rail band is compared with the
//     frame from before the prefix, not only the header.
//
// The list is also longer than 40 rows hold, and it is cut to the panes' rows
// so that it does not run up over the dock on the top two.
//
// Negative controls: with the which-key corners taken from the whole screen,
// the rail's rows change under the overlay; with the list cut to the screen's
// rows, the title lands over the dock.
func TestWhichKeySitsBesideTheRail(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	if out, err := tuiosCLI(t, base, "new", "e2e-wk", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-wk"}, shippedLooks: true})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1 && railHeaderColumn(s) >= 0
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached with the rail up: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v", err)
	}
	before := term.Screen()
	railX := railHeaderColumn(before)
	_, rows := before.Size()
	railBand := func(s tuitest.Screen, r int) string {
		runes := []rune(s.Line(r))
		if railX-1 >= len(runes) {
			return ""
		}
		return string(runes[railX-1:])
	}

	if err := term.SendKeys(tuitest.Ctrl('b')); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the which-key overlay", "Toggle tiling")
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v", err)
	}
	after := term.Screen()
	saveArtifact(t, term, artifactDir(t), "whichkey-beside-rail")
	// The dock and its rule are left out: the prefix changes what the dock says.
	for r := 2; r < rows; r++ {
		if got, want := railBand(after, r), railBand(before, r); got != want {
			t.Fatalf("row %d of the rail changed under the which-key overlay:\n got %q\nwant %q\n%s",
				r, got, want, term.Snapshot())
		}
	}
	// The list is longer than forty rows can hold, and it is cut to the
	// panes' rows rather than run up over the dock on the top two.
	titleRow := -1
	for r := 0; r < rows && titleRow < 0; r++ {
		if slices.Contains(strings.Fields(after.Line(r)), "prefix") {
			titleRow = r
		}
	}
	// Row 0 is the dock, row 1 its rule and row 2 the overlay's top pad.
	if titleRow < 3 {
		t.Fatalf("the which-key title is on row %d, over the dock on the top two rows\n%s", titleRow, term.Snapshot())
	}
}

// TestATallPanelStaysUnderTheDock opens the settings page at 120x40 with the
// shipped looks, where the dock is on the top two rows. The page is taller
// than the rows under the dock, and it was fitted to the whole screen: it ran
// up over the dock, and the ends of the dock's pills and buttons showed on
// both sides of the page's title. It is fitted to the rows under the dock now.
// The frame is saved under artifactDir.
//
// How this could pass wrongly, written down first:
//   - The page could be short enough to fit either way, so the test waits for
//     its last row, the footer, and requires it on the screen's last rows.
//   - The dock could change for its own reasons when a panel opens, so what
//     is compared is its left end, the workspace pills, which only a panel
//     drawn over them changes.
//
// Negative control: with panelRoomHeight returning the whole screen's rows
// and overlayOrigin placing from row 0, the settings page covers the dock's
// pills and the test fails. Either change alone keeps row 0 clear at this
// size, since the page is one row shorter than the screen.
func TestATallPanelStaysUnderTheDock(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	if out, err := tuiosCLI(t, base, "new", "e2e-tall", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-tall"}, shippedLooks: true})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1 && dockOnTop(s)
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached with the dock on top: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v", err)
	}
	pills := func(s tuitest.Screen) string {
		runes := []rune(s.Line(0))
		return string(runes[:min(len(runes), 12)])
	}
	before := pills(term.Screen())

	if err := term.SendKeys(",", "1"); err != nil {
		t.Fatalf("open settings: %v", err)
	}
	waitText(t, term, "the settings page", "Settings", "esc close")
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v", err)
	}
	s := term.Screen()
	saveArtifact(t, term, artifactDir(t), "settings-under-dock")
	if footer := rowWith(s, "esc close"); footer < 35 {
		t.Fatalf("the settings footer is on row %d: the page fits under the dock either way and this tests nothing\n%s",
			footer, term.Snapshot())
	}
	if got := pills(s); got != before {
		t.Fatalf("the settings page covered the dock's pills: row 0 began %q and now begins %q\n%s",
			before, got, term.Snapshot())
	}
}

// TestAPanelWiderThanThePanesCoversTheWholeRail opens the Inbox on an 80
// column screen with the shipped looks, where the rail takes the right 16
// columns and the Inbox is wider than the 64 left for the panes. Centred on
// the screen it covered all of the rail but its last column or two, which
// then showed down the panel's edge as fragments of the rail's rows: "e…",
// "1…", a lone "+". A panel that has to cover the rail now covers all of it.
// The frame is saved under artifactDir.
//
// How this could pass wrongly, written down first:
//   - The Inbox could fit beside the rail, and then it covers none of it and
//     nothing is tested. The panel's title is required to start left of the
//     rail and the rail's header to be on screen before the Inbox opens.
//   - A blank rail row would read the same as a covered one, so the check is
//     on the panel's own ground: every row from the title to the footer ends
//     in a cell painted with the panel's background.
//
// Negative control: with panelCenterX centring on the screen alone, the last
// column of the title row carries the rail's ground and the test fails.
func TestAPanelWiderThanThePanesCoversTheWholeRail(t *testing.T) {
	const cols, rows = 80, 24
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	for _, name := range []string{"e2e-home", "e2e-fan"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", name, err, out)
		}
	}
	term := startIn(t, base, startOpts{cols: cols, rows: rows, args: []string{"attach", "e2e-home"}, shippedLooks: true})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1 && railHeaderColumn(s) >= 0
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached with the rail up: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	railX := railHeaderColumn(term.Screen())

	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "e2e-fan", "errored",
		"--harness", "claude-code", "-m", "build failed on main"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	waitText(t, term, "the Inbox with the error", "Errored 1", "esc close")
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v", err)
	}
	s := term.Screen()
	saveArtifact(t, term, artifactDir(t), "inbox-over-rail")

	title, footer := -1, -1
	for r := range rows {
		line := s.Line(r)
		if title < 0 && strings.Contains(line, " Inbox") {
			title = r
		}
		if strings.Contains(line, "esc close") {
			footer = r
		}
	}
	if title < 0 || footer <= title {
		t.Fatalf("could not find the Inbox's title (%d) and footer (%d)\n%s", title, footer, term.Snapshot())
	}
	titleLine := s.Line(title)
	titleCol := len([]rune(titleLine[:strings.Index(titleLine, "Inbox")]))
	if titleCol >= railX {
		t.Fatalf("the Inbox starts at column %d, inside the rail at %d: it fits beside the rail and this tests nothing\n%s",
			titleCol, railX, term.Snapshot())
	}
	// The cell left of the title's pill is the panel's padding.
	ground := s.Cell(titleCol-2, title).Bg
	for r := title; r <= footer; r++ {
		if got := s.Cell(cols-1, r).Bg; got != ground {
			t.Fatalf("row %d ends in a cell of %+v, not the panel's %+v: the rail shows past the panel\n%s",
				r, got, ground, term.Snapshot())
		}
	}
}
