package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// tmlFrontDoc is a surface with one line on it that no shell prints.
const tmlFrontDoc = `<tml version="1" title="Build report">
  <col pad="1"><text>FRONT-MARKER build report</text></col>
</tml>
`

// TestTMLShowPaneOpensInFront opens a surface as a pane with tiling off, from
// a program in the focused pane, the way an agent shows its work. A new pane
// opens in front of the others, and so must this one. It opened under the
// focused window: the person saw the left half of it, and the rest, its
// footer with the owner's name included, was behind the pane they were in.
//
// How this could pass wrongly, written down first:
//   - Tiling could be on, and then no pane is over another. The test turns
//     it off with set-layout before it opens the surface.
//   - The surface could open where nothing covers it. Two panes split the
//     screen, the focused one on the right, and a pane with tiling off opens
//     in the middle, over both.
//   - Part of the surface could show and pass for all of it. The check is
//     the footer, which is the last thing drawn at the bottom right of the
//     box and so the first thing the focused pane covered.
//
// The frame is saved under artifactDir.
//
// Negative control: on origin/main, where the untiled sync path calls
// RecalcZOrder and so puts the focused pane on top, the footer is cut at the
// focused pane's border and the test fails.
func TestTMLShowPaneOpensInFront(t *testing.T) {
	const sess = "e2e-tmlfront"
	term, base := start(t, startOpts{cols: 120, rows: 36, args: []string{"new", sess}})
	waitBoot(t, term)
	// Two panes, so the focused one is over another, as after any day of
	// work with tiling off.
	for i := 1; i <= 2; i++ {
		if out, err := tuiosCLI(t, base, "run-command", "--session", sess, "NewWindow"); err != nil {
			t.Fatalf("run-command NewWindow: %v\n%s", err, out)
		}
		waitWindowCount(t, term, i, "the panes")
	}
	if out, err := tuiosCLI(t, base, "set-layout", "-s", sess, "--tiling", "false"); err != nil {
		t.Fatalf("turn tiling off: %v\n%s", err, out)
	}

	doc := writeTMLDoc(t, base, "report.tml", tmlFrontDoc)
	if out, err := tuiosCLI(t, base, "send-keys", "-s", sess, "-l",
		"clear; "+tuiosBin+" tml show "+doc+" --pane\r"); err != nil {
		t.Fatalf("run tml show in the pane: %v\n%s", err, out)
	}
	waitWindowCount(t, term, 3, "with the surface open")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		txt := s.Text()
		return strings.Contains(txt, "FRONT-MARKER build report") && strings.Contains(txt, "· Build report")
	}, uiTimeout); err != nil {
		saveTMLFrame(t, term, "surface-hidden")
		t.Fatalf("ASSERTION: the surface pane is not in front: its footer is not on the screen, so the focused pane is over it: %v\n%s", err, term.Snapshot())
	}
	saveTMLFrame(t, term, "surface-in-front")
}
