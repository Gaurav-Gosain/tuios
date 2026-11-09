package tuie2e

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// chromeCell is one cell of the rail or the dock as it was drawn.
type chromeCell struct {
	x, y    int
	content string
	fg, bg  tuitest.Color
}

// chromeCells are the drawn cells of the dock, the two rows at the top and
// the two at the foot of the screen, and of the rail, beyond its border on
// the fourth row on whichever side it is.
func chromeCells(s tuitest.Screen) []chromeCell {
	cols, rows := s.Size()
	var out []chromeCell
	add := func(x, y int) {
		c := s.Cell(x, y)
		if c.Content == "" || c.Content == " " {
			return
		}
		out = append(out, chromeCell{x: x, y: y, content: c.Content, fg: c.Fg, bg: c.Bg})
	}
	for _, y := range []int{0, 1, rows - 2, rows - 1} {
		for x := range cols {
			add(x, y)
		}
	}
	x0, x1 := 0, 0
	for x := range cols {
		if s.Cell(x, 3).Content != "│" {
			continue
		}
		if x < cols/2 {
			x0, x1 = 0, x
		} else if x1 == 0 {
			x0, x1 = x+1, cols
		}
	}
	for y := 2; y < rows-2; y++ {
		for x := x0; x < x1; x++ {
			add(x, y)
		}
	}
	return out
}

// TestAgentsViewLeavesTheChromeLit opens the agents view with the rail open
// and checks that the dock, and the rail where it shows, keep their colours:
// the view covers the pane area and is not a modal over the rest, so nothing behind it is
// dimmed.
//
// How it could pass wrongly, written down first:
//   - The view could cover the dock, leaving no cell to compare. The test
//     needs at least 20 dock cells that show the same character before and
//     after. The rail is compared where the view leaves it showing.
//   - A cell could change for a reason of its own, such as the mode pill.
//     Only cells that show the same character before and after are compared.
//
// Negative control: on main the agents view takes the modal scrim, and the
// rail's and the dock's cells are drawn darker, so the colour check fails.
func TestAgentsViewLeavesTheChromeLit(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	writeConfig(t, base, "[appearance.sidebar]\nenabled = true\n")
	for _, s := range []string{"home", "docs"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	for _, args := range [][]string{
		{"set-window", "-s", "docs", "-w", "0", "--name", "docs-cli"},
		{"set-agent-state", "idle", "-s", "docs", "-w", "docs-cli", "--harness", "claude-code"},
	} {
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 160, rows: 48, shippedLooks: true,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	_ = term.WaitStable(uiTimeout)
	time.Sleep(insertGuard)
	railShot(t, term, "chrome-before")
	before := chromeCells(term.Screen())

	sendKeys(t, term, tuitest.Ctrl('b'), "i")
	waitText(t, term, "the agents view", "Reply to docs-cli")
	_ = term.WaitStable(uiTimeout)
	time.Sleep(insertGuard)
	railShot(t, term, "chrome-agents-view")
	after := chromeCells(term.Screen())

	at := map[[2]int]chromeCell{}
	for _, c := range after {
		at[[2]int{c.x, c.y}] = c
	}
	_, rows := term.Screen().Size()
	var railSame, dockSame, changed int
	var first chromeCell
	for _, b := range before {
		a, ok := at[[2]int{b.x, b.y}]
		if !ok || a.content != b.content {
			continue
		}
		if b.y < 2 || b.y >= rows-2 {
			dockSame++
		} else {
			railSame++
		}
		if a.fg != b.fg || a.bg != b.bg {
			if changed == 0 {
				first = b
			}
			changed++
		}
	}
	t.Logf("rail cells compared %d, dock cells compared %d, changed colour %d", railSame, dockSame, changed)
	if dockSame < 20 {
		t.Fatalf("ASSERTION: too few dock cells (%d) show the same character before and after to compare\n%s", dockSame, term.Snapshot())
	}
	if changed > 0 {
		a := at[[2]int{first.x, first.y}]
		t.Fatalf("ASSERTION: the agents view dimmed %d rail and dock cells, first %q at %d,%d: fg %+v to %+v, bg %+v to %+v\n%s",
			changed, first.content, first.x, first.y, first.fg, a.fg, first.bg, a.bg, term.Snapshot())
	}
}
