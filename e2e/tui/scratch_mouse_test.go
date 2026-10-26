package tuie2e

import (
	"encoding/json"
	"fmt"
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

// With focus_follows_mouse on, moving the pointer over the tiles must not take
// the focus from the shown scratch terminal: it stays on the screen and the
// keys still go to it.
func TestScratchStaysShownUnderHover(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\nfocus_follows_mouse = true\n")
	term := startIn(t, base, startOpts{cols: 160, rows: 45, args: []string{"new", "work"}})
	waitBoot(t, term)
	for range 2 {
		newWindow(t, term)
	}
	enableTiling(t, term)
	time.Sleep(500 * time.Millisecond)
	toggleScratch(t, term)
	waitScratch(t, term, base, false, false, "show over the tiles")
	typeUntil(t, term, "echo HOVER-$((6*7))", "HOVER-42")
	for col := 2; col < 60; col += 4 {
		mouseHover(t, term, col, 41)
		time.Sleep(30 * time.Millisecond)
	}
	time.Sleep(700 * time.Millisecond)
	// The screen, not the daemon's list: the hover hid the popup on this
	// client before any state went out.
	if !strings.Contains(term.Screen().Text(), "HOVER-42") {
		t.Fatalf("a hover hid the scratch terminal\n%s", term.Snapshot())
	}
	row, _ := scratchRowOf(t, base)
	typeUntil(t, term, "echo STILL-$((2*4))", "STILL-8")
	if pane, err := tuiosCLI(t, base, "capture-pane", "-s", "work", "-w", row.ID); err != nil || !strings.Contains(pane, "STILL-8") {
		t.Fatalf("the keys after the hover did not reach the scratch terminal (%v):\n%s", err, pane)
	}
	t.Logf("the scratch terminal after the hover:\n%s", term.Snapshot())
}

// floatRect is one row of list-windows --json for session work: where the
// daemon says a pane is.
type floatRect struct {
	ID       string `json:"window_id"`
	X        int    `json:"x"`
	Y        int    `json:"y"`
	W        int    `json:"width"`
	H        int    `json:"height"`
	Floating bool   `json:"floating"`
}

func (r floatRect) String() string {
	return fmt.Sprintf("x=%d y=%d w=%d h=%d", r.X, r.Y, r.W, r.H)
}

// floatAndTile is the floating pane and the one tiled pane of session work,
// read once the daemon has a state with exactly one of each.
func floatAndTile(t *testing.T, base, what string) (float, tile floatRect) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	var out string
	for time.Now().Before(deadline) {
		var err error
		out, err = tuiosCLI(t, base, "list-windows", "--json", "--session", "work")
		if err == nil {
			var res struct {
				Windows []floatRect `json:"windows"`
			}
			if json.Unmarshal([]byte(out), &res) == nil && len(res.Windows) == 2 &&
				res.Windows[0].Floating != res.Windows[1].Floating {
				for _, w := range res.Windows {
					if w.Floating {
						float = w
					} else {
						tile = w
					}
				}
				return float, tile
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s: no state with one floating and one tiled pane:\n%s", what, out)
	return float, tile
}

// The report: in tiling mode with one tiled pane, a pane floated with the
// leader, t, f. The alt+right drag that resizes a pane did nothing to the
// float on the first try, and once it did resize, it also resized the tiled
// pane behind it, which took the float's row and height. The resize ran the
// tiling path for a pane that is not in the tiling: that path holds an edge
// that sits on the screen boundary still (the float starts in the slot it was
// tiled in, so most of its edges are on a boundary) and moves every tiled pane
// that shares a line with the edge it drags. A left drag on the float's own
// edge took the same path, and a press there moved the float instead. A peer's
// sync that moved the focus mid-resize handed the rest of the gesture to the
// tiled pane.
func TestFloatMouseResizeKeepsTheTiles(t *testing.T) {
	term, base := scratchOverTiles(t, 2)
	if err := term.SendKeys(tuitest.Ctrl('b'), "t", "f"); err != nil {
		t.Fatalf("float the pane: %v", err)
	}
	if err := term.WaitForText("Window: floating", uiTimeout); err != nil {
		t.Fatalf("the pane never floated: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(500 * time.Millisecond)
	float, tile := floatAndTile(t, base, "after the float")
	t.Logf("after the float: float %v, tile %v", float, tile)
	saveFrame(t, term, "float-resize-0-floated")

	// Two drags, each from inside the float toward its middle: first from the
	// bottom-right quarter, which shrinks the far edges, then from the
	// top-left quarter, which shrinks the near ones.
	for i, d := range []struct {
		name    string
		topLeft bool
		dx, dy  int
	}{
		{name: "bottom-right", dx: -12, dy: -6},
		{name: "top-left", topLeft: true, dx: 8, dy: 4},
	} {
		before := float
		fromX, fromY := before.X+before.W-4, before.Y+before.H-3
		wantW, wantH := before.W+d.dx, before.H+d.dy
		if d.topLeft {
			fromX, fromY = before.X+4, before.Y+3
			wantW, wantH = before.W-d.dx, before.H-d.dy
		}
		mouseDrag(t, term, fromX, fromY, fromX+d.dx, fromY+d.dy, tuitest.MouseRight, tuitest.ModAlt)
		time.Sleep(900 * time.Millisecond)
		var gotTile floatRect
		float, gotTile = floatAndTile(t, base, "after the "+d.name+" drag")
		t.Logf("after the %s drag: float %v, tile %v", d.name, float, gotTile)
		saveFrame(t, term, fmt.Sprintf("float-resize-%d-%s", i+1, d.name))
		if float.W != wantW || float.H != wantH {
			t.Errorf("the %s drag by %d,%d took the float from %v to %v, want w=%d h=%d\n%s",
				d.name, d.dx, d.dy, before, float, wantW, wantH, term.Snapshot())
		}
		if gotTile != tile {
			t.Fatalf("the %s drag on the float moved the tiled pane from %v to %v\n%s",
				d.name, tile, gotTile, term.Snapshot())
		}
	}

	// A left drag on the float's bottom edge, a line the tiled pane also ends
	// on when the float is in the slot it was tiled in.
	before := float
	col, row := before.X+before.W/2, before.Y+before.H-1
	mouseDrag(t, term, col, row, col, row-5, tuitest.MouseLeft, 0)
	time.Sleep(900 * time.Millisecond)
	float, gotTile := floatAndTile(t, base, "after the bottom edge drag")
	t.Logf("after the bottom edge drag: float %v, tile %v", float, gotTile)
	saveFrame(t, term, "float-resize-3-bottom-edge")
	if float.H != before.H-5 || float.Y != before.Y {
		t.Errorf("the bottom edge drag by -5 took the float from %v to %v, want y=%d h=%d\n%s",
			before, float, before.Y, before.H-5, term.Snapshot())
	}
	if gotTile != tile {
		t.Fatalf("the bottom edge drag on the float moved the tiled pane from %v to %v\n%s",
			tile, gotTile, term.Snapshot())
	}

	// A second client moves the focus to the tiled pane while the float is
	// mid-resize. The rest of the gesture still belongs to the float.
	peer := attachIn(t, base, "work", startOpts{cols: 160, rows: 45})
	time.Sleep(time.Second)
	before = float
	col, row = before.X+before.W-4, before.Y+before.H-3
	mousePress(t, term, col, row, tuitest.MouseRight, tuitest.ModAlt)
	for i := 1; i <= 4; i++ {
		mouseMotion(t, term, col+i, row+i, tuitest.MouseRight, tuitest.ModAlt)
	}
	time.Sleep(300 * time.Millisecond)
	if err := peer.SendKeys("\t"); err != nil {
		t.Fatalf("peer tab: %v", err)
	}
	time.Sleep(800 * time.Millisecond)
	for i := 5; i <= 8; i++ {
		mouseMotion(t, term, col+i, row+i, tuitest.MouseRight, tuitest.ModAlt)
	}
	mouseRelease(t, term, col+8, row+8, tuitest.MouseRight, tuitest.ModAlt)
	time.Sleep(900 * time.Millisecond)
	float, gotTile = floatAndTile(t, base, "after the resize across a peer's sync")
	t.Logf("after the resize across a peer's sync: float %v, tile %v", float, gotTile)
	saveFrame(t, term, "float-resize-4-peer-sync")
	if float.W != before.W+8 || float.H != before.H+8 {
		t.Errorf("the resize across a peer's sync took the float from %v to %v, want w=%d h=%d\n%s",
			before, float, before.W+8, before.H+8, term.Snapshot())
	}
	if gotTile != tile {
		t.Fatalf("the resize across a peer's sync moved the tiled pane from %v to %v\n%s",
			tile, gotTile, term.Snapshot())
	}
	alive(t, term, "after the float resizes")
}

// The state the report ended in: the tiled pane at the float's row and
// height. It takes a float with an edge on a tiled pane's line, or within the
// one row the line match allows without shared borders, and a drag of that
// edge. The tiled pane's edge went with it. A float in its tiled slot has its
// top and bottom on the tiled pane's lines. Moved one row down, its top is
// still in reach of the match and no longer held as a screen edge. Run on the
// shipped looks and on own borders with the dock on top, the settings of the
// report.
func TestFloatEdgeResizeKeepsTheTile(t *testing.T) {
	looks := []struct {
		name    string
		shipped bool
		config  string
	}{
		{name: "shipped looks", shipped: true},
		{name: "own borders", config: "[appearance]\nshared_borders = false\ndockbar_position = \"top\"\n"},
	}
	// down is how far the float moves before the resize. bottom picks a left
	// drag on the bottom border, 5 rows up, instead of an alt+right drag from
	// the top-left quarter, 15 rows down. The top border is the title bar, a
	// move handle, so the top edge has only the alt+right drag.
	cases := []struct {
		name   string
		down   int
		bottom bool
	}{
		{name: "top in its slot"},
		{name: "top one row below its slot", down: 1},
		{name: "bottom border in its slot", bottom: true},
	}
	for _, l := range looks {
		for _, c := range cases {
			name := l.name + "/" + c.name
			t.Run(name, func(t *testing.T) {
				base := t.TempDir()
				if l.config != "" {
					writeConfig(t, base, l.config)
				}
				term := startIn(t, base, startOpts{cols: 160, rows: 45, shippedLooks: l.shipped, args: []string{"new", "work"}})
				waitBoot(t, term)
				newWindow(t, term)
				newWindow(t, term)
				enableTiling(t, term)
				time.Sleep(500 * time.Millisecond)
				if err := term.SendKeys(tuitest.Ctrl('b'), "t", "f"); err != nil {
					t.Fatalf("float the pane: %v", err)
				}
				if err := term.WaitForText("Window: floating", uiTimeout); err != nil {
					t.Fatalf("the pane never floated: %v\n%s", err, term.Snapshot())
				}
				time.Sleep(500 * time.Millisecond)
				float, tile := floatAndTile(t, base, "after the float")
				t.Logf("after the float: float %v, tile %v", float, tile)
				if c.down > 0 {
					col := float.X + 10
					mouseDrag(t, term, col, float.Y, col, float.Y+c.down, tuitest.MouseLeft, tuitest.ModAlt)
					time.Sleep(800 * time.Millisecond)
					var gotTile floatRect
					float, gotTile = floatAndTile(t, base, "after the move")
					t.Logf("after the move: float %v, tile %v", float, gotTile)
					if float.Y != tile.Y+c.down || gotTile != tile {
						t.Fatalf("the move left the float at %v and the tile at %v, want the float at y=%d and the tile at %v",
							float, gotTile, tile.Y+c.down, tile)
					}
				}
				frame := "float-edge-" + strings.NewReplacer(" ", "-", "/", "-").Replace(name)
				saveFrame(t, term, frame+"-0-before")

				before := float
				if c.bottom {
					col, row := before.X+before.W/2, before.Y+before.H-1
					mouseDrag(t, term, col, row, col, row-5, tuitest.MouseLeft, 0)
				} else {
					col, row := before.X+4, before.Y+1
					mouseDrag(t, term, col, row, col, row+15, tuitest.MouseRight, tuitest.ModAlt)
				}
				time.Sleep(900 * time.Millisecond)
				float, gotTile := floatAndTile(t, base, "after the edge drag")
				t.Logf("after the edge drag: float %v, tile %v", float, gotTile)
				saveFrame(t, term, frame+"-1-after")
				switch {
				case c.bottom && (float.Y != before.Y || float.H != before.H-5):
					t.Errorf("the bottom border drag by -5 took the float from %v to %v, want y=%d h=%d\n%s",
						before, float, before.Y, before.H-5, term.Snapshot())
				case !c.bottom && float.Y < before.Y+14:
					t.Errorf("the top drag by 15 took the float from %v to %v, want its top at about y=%d\n%s",
						before, float, before.Y+15, term.Snapshot())
				}
				if gotTile != tile {
					t.Fatalf("the edge drag on the float moved the tiled pane from %v to %v\n%s",
						tile, gotTile, term.Snapshot())
				}
				alive(t, term, "after the edge drag")
			})
		}
	}
}
