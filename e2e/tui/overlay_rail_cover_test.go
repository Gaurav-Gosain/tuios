package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

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
