package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The scratch terminal over a tiled layout, driven with the mouse, on the
// shipped looks (sidebar, dock on top, shared borders).
//
// The report: in tiled mode with the popup shown, a mouse drag shrank the
// popup to a small box, left the tile in half the screen, and moved the rail's
// focus mark to a pane that did not get the keys. The frame draws a floating
// pane above the tiles, but the hit test compared the raw Z, which a daemon-
// made popup does not raise above the tiles. So a press on the popup landed on
// the tile under it, and the drag and resize code then acted on panes the user
// was not pointing at.

// scratchOverTiles starts work with n tiled panes and the scratch terminal
// shown over them, in window mode.
func scratchOverTiles(t *testing.T, n int) (*tuitest.Terminal, string) {
	t.Helper()
	base := t.TempDir()
	term := startIn(t, base, startOpts{cols: 160, rows: 45, shippedLooks: true, args: []string{"new", "work"}})
	waitBoot(t, term)
	for range n {
		newWindow(t, term)
	}
	enableTiling(t, term)
	time.Sleep(500 * time.Millisecond)
	return term, base
}

// scratchBox is where the popup's top and bottom borders are drawn: the row
// and column of the top-left corner and the row of the bottom border.
func scratchBox(s tuitest.Screen) (top, left, bottom int) {
	_, rows := s.Size()
	top, left, bottom = -1, -1, -1
	for r := range rows {
		line := []rune(s.Line(r))
		title := strings.Index(string(line[:min(len(line), 136)]), "scratch")
		if title < 0 {
			continue
		}
		col := len([]rune(string(line[:min(len(line), 136)])[:title]))
		for c := col; c >= 0; c-- {
			if line[c] == '╭' {
				left = c
				break
			}
		}
		if left < 0 {
			continue
		}
		top = r
		for b := r + 1; b < rows; b++ {
			if l := []rune(s.Line(b)); left < len(l) && l[left] == '╰' {
				bottom = b
				break
			}
		}
		return top, left, bottom
	}
	return top, left, bottom
}

// tileRows is the pane region's border rows with every pane's content left
// out: the rows that change when the tiling changes.
func tileRows(s tuitest.Screen) string {
	_, rows := s.Size()
	var b strings.Builder
	for r := range rows {
		line := s.Line(r)
		if strings.Contains(line, "╭") || strings.Contains(line, "╰") {
			b.WriteString(line[:min(len(line), 100)])
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// sidebarLists reports whether the rail, right of column 136, shows text.
func sidebarLists(s tuitest.Screen, text string) bool {
	_, rows := s.Size()
	for r := range rows {
		line := []rune(s.Line(r))
		if len(line) > 137 && strings.Contains(string(line[137:]), text) {
			return true
		}
	}
	return false
}

func TestScratchMouseKeepsTheLayout(t *testing.T) {
	for _, n := range []int{1, 2} {
		t.Run(map[int]string{1: "one tile", 2: "two tiles"}[n], func(t *testing.T) {
			term, base := scratchOverTiles(t, n)
			layout := tileRows(term.Screen())

			toggleScratch(t, term)
			waitScratch(t, term, base, false, false, "show over the tiles")
			typeUntil(t, term, "echo TILED-$((6*7))", "TILED-42")
			windowManagementMode(t, term)
			top, left, bottom := scratchBox(term.Screen())
			if top < 0 || bottom < 0 {
				t.Fatalf("no popup on screen (%d %d %d)\n%s", top, left, bottom, term.Snapshot())
			}
			if sidebarLists(term.Screen(), "scratch") {
				t.Errorf("the rail lists the shown scratch terminal\n%s", term.Snapshot())
			}

			// A right drag and a left drag inside the popup, and a left drag
			// on its left edge. None of them may move or size anything.
			for _, d := range []struct {
				x0, y0, x1, y1 int
				b              tuitest.MouseButton
			}{
				{30, 30, 80, 10, tuitest.MouseRight},
				{80, 20, 40, 30, tuitest.MouseLeft},
				{left, 20, left + 25, 20, tuitest.MouseLeft},
			} {
				mouseDrag(t, term, d.x0, d.y0, d.x1, d.y1, d.b, 0)
				time.Sleep(700 * time.Millisecond)
				gt, gl, gb := scratchBox(term.Screen())
				if gt != top || gl != left || gb != bottom {
					t.Fatalf("a drag from %d,%d to %d,%d moved the popup from rows %d-%d col %d to rows %d-%d col %d\n%s",
						d.x0, d.y0, d.x1, d.y1, top, bottom, left, gt, gb, gl, term.Snapshot())
				}
				if !strings.Contains(term.Screen().Text(), "TILED-42") {
					t.Fatalf("the popup lost its text after a drag\n%s", term.Snapshot())
				}
			}
			t.Logf("the popup after the drags:\n%s", term.Snapshot())

			// A click outside the popup, on the tile below it, hides it.
			mouseClick(t, term, 5, 42, tuitest.MouseLeft, 0)
			waitGone(t, term, "a click outside the popup", "TILED-42")
			waitScratch(t, term, base, true, false, "after the click outside")
			time.Sleep(500 * time.Millisecond)
			if got := tileRows(term.Screen()); got != layout {
				t.Fatalf("the tiles changed:\nbefore\n%s\nafter\n%s\n%s", layout, got, term.Snapshot())
			}
			if sidebarLists(term.Screen(), "scratch") {
				t.Errorf("the rail lists the hidden scratch terminal\n%s", term.Snapshot())
			}
			t.Logf("the layout after the hide:\n%s", term.Snapshot())
			alive(t, term, "after the mouse round trip")
		})
	}
}
