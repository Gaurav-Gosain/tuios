package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

const cornerGlyphs = "╭┌╔┏"

func cornerAt(s tuitest.Screen, col, row int) bool {
	line := []rune(s.Line(row))
	return col >= 0 && col < len(line) && strings.ContainsRune(cornerGlyphs, line[col])
}

func dumpFrame(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	dir := os.Getenv("TUIOS_E2E_FRAMES")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("dumpFrame: %v", err)
	}
	_ = os.WriteFile(filepath.Join(dir, name+".txt"), []byte(term.Snapshot()), 0o644)
	_ = os.WriteFile(filepath.Join(dir, name+".ansi"), []byte(term.SnapshotStyled()), 0o644)
}

func tiledDragBoard(t *testing.T, session, layout string) (*tuitest.Terminal, string, winRect, winRect) {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, "[startup]\ndaemon = true\ntiled = true\nlayout = '"+layout+"'\n")
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", session}})
	killDaemon(t, base)
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 2, "tiled drag setup")
	rects := waitForSettledGeometry(t, base, 2)
	left, right := rects[0], rects[1]
	if right.X < left.X {
		left, right = right, left
	}
	if left.X+left.Width > right.X {
		t.Fatalf("the two panes are not side by side: %+v and %+v", left, right)
	}
	if !cornerAt(term.Screen(), right.X, right.Y) {
		t.Fatalf("no pane corner drawn at the right pane's slot (%d,%d)\n%s", right.X, right.Y, term.Snapshot())
	}
	return term, base, left, right
}

func dragTitle(t *testing.T, term *tuitest.Terminal, pane winRect, dx, dy int) (col, row int) {
	t.Helper()
	col, row = pane.X+pane.Width/2, pane.Y
	mousePress(t, term, col, row, tuitest.MouseLeft, 0)
	steps := max(abs(dx), abs(dy))
	for i := 1; i <= steps; i++ {
		mouseMotion(t, term, col+dx*i/steps, row+dy*i/steps, tuitest.MouseLeft, 0)
	}
	return col + dx, row + dy
}

// TestATitleDragMovesATiledPaneInEveryLayout is the regression for the
// scrolling strip, with the other two layouts as the control that says what a
// moving pane looks like. Mid-drag the pane's corner is drawn where the pointer
// carried it and its slot stands empty; the drop on the neighbour swaps the two
// panes as the daemon has them.
func TestATitleDragMovesATiledPaneInEveryLayout(t *testing.T) {
	for _, layout := range []string{"bsp", "master-stack", "scrolling"} {
		t.Run(layout, func(t *testing.T) {
			term, base, left, right := tiledDragBoard(t, "tdrag-"+layout, layout)

			const dx, dy = -25, 12
			col, row := dragTitle(t, term, right, dx, dy)
			time.Sleep(400 * time.Millisecond)
			dumpFrame(t, term, layout+"-1-mid-drag")

			s := term.Screen()
			if !cornerAt(s, right.X+dx, right.Y+dy) {
				t.Errorf("%s: after dragging the title bar by (%d,%d) no pane corner is drawn at (%d,%d): "+
					"the pane is not following the pointer\n%s", layout, dx, dy, right.X+dx, right.Y+dy, term.Snapshot())
			}
			if cornerAt(s, right.X, right.Y) {
				t.Errorf("%s: the pane is still drawn in its slot at (%d,%d) mid-drag\n%s", layout, right.X, right.Y, term.Snapshot())
			}

			tx, ty := left.X+left.Width/2, left.Y+left.Height/2
			steps := max(abs(tx-col), abs(ty-row))
			for i := 1; i <= steps; i++ {
				mouseMotion(t, term, col+(tx-col)*i/steps, row+(ty-row)*i/steps, tuitest.MouseLeft, 0)
			}
			mouseRelease(t, term, tx, ty, tuitest.MouseLeft, 0)
			time.Sleep(time.Second)
			dumpFrame(t, term, layout+"-2-after-drop")

			after := waitForSettledGeometry(t, base, 2)
			byID := map[string]winRect{}
			for _, r := range after {
				byID[r.ID] = r
			}
			if got := byID[right.ID]; got.X != left.X || got.Y != left.Y {
				t.Errorf("%s: dropped on the left pane, the dragged pane sits at (%d,%d), want the left slot (%d,%d)",
					layout, got.X, got.Y, left.X, left.Y)
			}
			if got := byID[left.ID]; got.X != right.X || got.Y != right.Y {
				t.Errorf("%s: after the swap the left pane sits at (%d,%d), want the right slot (%d,%d)",
					layout, got.X, got.Y, right.X, right.Y)
			}
			for a := range after {
				for b := a + 1; b < len(after); b++ {
					if geomOverlap(after[a], after[b]) {
						t.Errorf("%s: the drop left two panes overlapping: %+v and %+v", layout, after[a], after[b])
					}
				}
			}
			alive(t, term, "after a title drag")
		})
	}
}

// TestAPeerSyncMidDragLeavesThePaneUnderThePointer: a second client attached to
// the same session presses a key while the first is mid-drag. The sync that
// carries the peer's focus used to put the dragged pane back in its slot and
// hand the rest of the gesture to the pane the peer had focused, so the drop
// left that pane parked at the pointer over the tiled layout.
func TestAPeerSyncMidDragLeavesThePaneUnderThePointer(t *testing.T) {
	term, base, left, right := tiledDragBoard(t, "tdrag-peer", "bsp")
	before := waitForSettledGeometry(t, base, 2)
	peer := attachIn(t, base, "tdrag-peer", startOpts{cols: 120, rows: 40})
	time.Sleep(time.Second)

	const dx, dy = -25, 12
	col, row := dragTitle(t, term, right, dx, dy)
	time.Sleep(300 * time.Millisecond)
	if !cornerAt(term.Screen(), right.X+dx, right.Y+dy) {
		t.Fatalf("the pane never followed the pointer before the peer did anything\n%s", term.Snapshot())
	}

	if err := peer.SendKeys("\t"); err != nil {
		t.Fatalf("peer tab: %v", err)
	}
	time.Sleep(800 * time.Millisecond)
	dumpFrame(t, term, "peer-1-after-sync")
	if !cornerAt(term.Screen(), right.X+dx, right.Y+dy) {
		t.Errorf("the peer's sync took the pane out from under the pointer: no corner at (%d,%d)\n%s",
			right.X+dx, right.Y+dy, term.Snapshot())
	}

	for i := 1; i <= 5; i++ {
		mouseMotion(t, term, col-i, row, tuitest.MouseLeft, 0)
	}
	time.Sleep(300 * time.Millisecond)
	s := term.Screen()
	if !cornerAt(s, right.X+dx-5, right.Y+dy) {
		t.Errorf("after the peer's sync the grabbed pane stopped following the pointer: no corner at (%d,%d)\n%s",
			right.X+dx-5, right.Y+dy, term.Snapshot())
	}
	if !cornerAt(s, left.X, left.Y) {
		t.Errorf("after the peer's sync the other pane was dragged instead: no corner at its slot (%d,%d)\n%s",
			left.X, left.Y, term.Snapshot())
	}
	mouseRelease(t, term, right.X+right.Width/2, right.Y+right.Height/2, tuitest.MouseLeft, 0)
	time.Sleep(time.Second)
	dumpFrame(t, term, "peer-2-after-drop")

	after := waitForSettledGeometry(t, base, 2)
	if !sameGeometry(before, after) {
		t.Errorf("a drop back in the pane's own slot changed the layout:\n before %+v\n after  %+v", before, after)
	}
	alive(t, term, "after a drag across a peer's sync")
}
