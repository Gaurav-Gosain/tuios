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
// The test saves the board at 79x30 (the list), 120x40 and 200x55 as frames,
// the widest last, so a build without the change still saves all three.
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
		if size.cols == 200 {
			waitText(t, term, "the unheld cards", "No pane holds it", "Waits for verify", "Shipped  1")
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return strings.Count(s.Text(), "free") == 2
			}, uiTimeout); err != nil {
				t.Errorf("ASSERTION: the board does not say free for only the Todo and Plan cards: %v\n%s", err, term.Snapshot())
			}
		}
	}
}
