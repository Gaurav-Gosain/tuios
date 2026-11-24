package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestTheBoardSaysWhatAnUnheldCardWaitsFor: cards with no holder in each
// stage. Only a card in Todo or Plan says free. A card in Doing says no pane
// holds it, a card in Verify with no verify run says Waits for verify, and
// a card in Shipped says Shipped.
//
// How it could pass wrongly: the words could come from the column heads, so
// the test waits for the sentences no head has, and checks that "free" shows
// only twice, for the Todo and the Plan card.
//
// Negative control, in NEGATIVE_CONTROLS.md: on a build where every card
// with no holder says free, "No pane holds it" is not on the board.
//
// The test reads the list at 79x30 and the columns at 200x55, and saves the
// board at 79x30, 120x40 and 200x55 as frames.
func TestTheBoardSaysWhatAnUnheldCardWaitsFor(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	mustCLI(t, base, "new", "S", "--detach")
	waitForSessionInfo(t, base, "S")
	for _, c := range []struct{ stage, title string }{
		{"todo", "write the changelog"},
		{"plan", "plan the cache"},
		{"doing", "port the parser"},
		{"verify", "check the retry"},
		{"shipped", "ship the docs"},
	} {
		mustCLI(t, base, "task", "add", "-s", "S", "--stage", c.stage, c.title)
	}
	for _, size := range []struct {
		name       string
		cols, rows int
	}{{"79x30", 79, 30}, {"120x40", 120, 40}, {"200x55", 200, 55}} {
		term := attachIn(t, base, "S", startOpts{cols: size.cols, rows: size.rows, shippedLooks: true})
		if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
			t.Fatal(err)
		}
		waitText(t, term, "the agents view", "b board")
		if err := term.SendKeys("b"); err != nil {
			t.Fatal(err)
		}
		waitText(t, term, "the board", "write the changelog")
		_ = term.WaitStable(uiTimeout)
		saveCockpitFrames(t, term, "board-unheld-"+size.name)
		if size.cols == 120 {
			// The columns at this width show Todo to Verify only.
			continue
		}
		// The columns at 200 and the list at 79 each show every card.
		waitText(t, term, "the unheld cards", "No pane holds it", "Waits for verify", "Shipped  1")
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			// free for the Todo and the Plan card only, and Shipped for the
			// Shipped card as well as its head.
			return strings.Count(s.Text(), "free") == 2 && strings.Count(s.Text(), "Shipped") == 2
		}, uiTimeout); err != nil {
			t.Errorf("ASSERTION: the board at %s does not say free for only the Todo and Plan cards and Shipped on the Shipped card: %v\n%s", size.name, err, term.Snapshot())
		}
	}
}
