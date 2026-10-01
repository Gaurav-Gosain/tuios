package layout

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The ways the master layout can fail, written down before the code:
//
//  1. The defaults (master on the left, one master, the grid on) move a pane
//     that the layout before master positions existed put somewhere else. Every
//     existing user would see a different screen after an upgrade.
//  2. A rectangle leaves the region or has no cells.
//  3. Two rectangles overlap.
//  4. The count of rectangles is not the count of panes.
//  5. The master is not on the side that was asked for.
//  6. The ratio read back from a layout does not lay the same layout out
//     again, so a resize that SyncMasterStackFromGeometry records moves the
//     panes on the next retile.
//
// FuzzMasterLayout checks all six for every position, master count, ratio,
// size and gap the fuzzer reaches. The seeds run in the ordinary suite.

// legacyMasterStack is CalculateMasterStackLayout as it was before master
// positions existed, kept verbatim as the oracle for failure 1.
func legacyMasterStack(n, screenWidth, usableHeight, topMargin int, masterRatio, stackRatio float64, gap int) []TileLayout {
	if n == 0 {
		return nil
	}
	layouts := make([]TileLayout, 0, n)
	if masterRatio <= 0 {
		masterRatio = float64(config.MasterRatioDefault) / 100
	}
	masterRatio = clampSplitRatio(masterRatio)
	switch n {
	case 1:
		layouts = append(layouts, TileLayout{X: 0, Y: topMargin, Width: screenWidth, Height: usableHeight})
	case 2:
		if screenWidth >= usableHeight*cellAspect {
			near, far := splitByRatio(0, screenWidth, masterRatio, gap)
			layouts = append(layouts,
				TileLayout{X: near.Pos, Y: topMargin, Width: near.Size, Height: usableHeight},
				TileLayout{X: far.Pos, Y: topMargin, Width: far.Size, Height: usableHeight},
			)
			break
		}
		near, far := splitByRatio(topMargin, usableHeight, masterRatio, gap)
		layouts = append(layouts,
			TileLayout{X: 0, Y: near.Pos, Width: screenWidth, Height: near.Size},
			TileLayout{X: 0, Y: far.Pos, Width: screenWidth, Height: far.Size},
		)
	case 3:
		master, stack := splitByRatio(0, screenWidth, masterRatio, gap)
		rows := spans(topMargin, usableHeight, 2, gap)
		if stackRatio > 0 {
			top, bottom := splitByRatio(topMargin, usableHeight, clampSplitRatio(stackRatio), gap)
			rows = []span{top, bottom}
		}
		layouts = append(layouts,
			TileLayout{X: master.Pos, Y: topMargin, Width: master.Size, Height: usableHeight},
			TileLayout{X: stack.Pos, Y: rows[0].Pos, Width: stack.Size, Height: rows[0].Size},
			TileLayout{X: stack.Pos, Y: rows[1].Pos, Width: stack.Size, Height: rows[1].Size},
		)
	default:
		cols := gridColumns(n)
		rowCount := (n + cols - 1) / cols
		rows := spans(topMargin, usableHeight, rowCount, gap)
		for row := range rowCount {
			inRow := min(cols, n-row*cols)
			cells := spans(0, screenWidth, inRow, gap)
			for col := range inRow {
				layouts = append(layouts, TileLayout{X: cells[col].Pos, Y: rows[row].Pos, Width: cells[col].Size, Height: rows[row].Size})
			}
		}
	}
	return layouts
}

func checkMasterLayout(t *testing.T, n, w, h, top int, p MasterParams) {
	t.Helper()
	what := fmt.Sprintf("n=%d %dx%d top=%d %+v", n, w, h, top, p)
	got := CalculateMasterLayout(n, w, h, top, p)

	// 4
	if len(got) != n {
		t.Fatalf("%s: %d rectangles", what, len(got))
	}
	// 2 and 3
	for _, r := range outside(got, w, h, top) {
		t.Errorf("%s: %+v is outside the region", what, r)
	}
	for _, pair := range overlapping(got) {
		t.Errorf("%s: %+v and %+v overlap", what, pair[0], pair[1])
	}
	if t.Failed() {
		t.FailNow()
	}

	// 1
	if (p.Position == "" || p.Position == config.MasterPositionLeft) && p.Count <= 1 && p.Grid {
		want := legacyMasterStack(n, w, h, top, p.Ratio, p.StackRatio, p.Gap)
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: pane %d at %+v, the layout before master positions put it at %+v", what, i, got[i], want[i])
			}
		}
	}

	shape := resolveMasterShape(n, w, h, p)
	if shape.grid || shape.stack == 0 {
		return
	}

	// 5. Every master sits on the asked side of every stack pane.
	masters, stack := got[:shape.masters], got[shape.masters:]
	for _, mr := range masters {
		for i, sr := range stack {
			var ok bool
			switch shape.position {
			case config.MasterPositionLeft:
				ok = mr.X+mr.Width <= sr.X
			case config.MasterPositionRight:
				ok = sr.X+sr.Width <= mr.X
			case config.MasterPositionTop:
				ok = mr.Y+mr.Height <= sr.Y
			case config.MasterPositionBottom:
				ok = sr.Y+sr.Height <= mr.Y
			case config.MasterPositionCenter:
				// Alternating: the first stack pane right, the next left.
				if i%2 == 0 {
					ok = mr.X+mr.Width <= sr.X
				} else {
					ok = sr.X+sr.Width <= mr.X
				}
			}
			if !ok {
				t.Fatalf("%s: master %+v is not %s of stack pane %d %+v", what, mr, shape.position, i, sr)
			}
		}
	}

	// 6. The ratios read back lay the same panes out again.
	rects := make([]Rect, n)
	for i, l := range got {
		rects[i] = Rect{X: l.X, Y: l.Y, W: l.Width, H: l.Height}
	}
	ratio, stackRatio, ok := MasterRatiosFrom(rects, Rect{X: 0, Y: top, W: w, H: h}, p)
	if !ok {
		t.Fatalf("%s: no ratio read back from the tiler's own layout", what)
	}
	q := p
	q.Ratio = ratio
	if stackRatio > 0 {
		q.StackRatio = stackRatio
	}
	again := CalculateMasterLayout(n, w, h, top, q)
	for i := range got {
		if got[i] != again[i] {
			t.Fatalf("%s: read back ratio %.4f stack %.4f moves pane %d from %+v to %+v",
				what, ratio, stackRatio, i, got[i], again[i])
		}
	}
}

func FuzzMasterLayout(f *testing.F) {
	positions := config.MasterPositions
	for _, pos := range positions {
		for n := 1; n <= 7; n++ {
			f.Add(n, 160, 46, 1, 1, uint8(50), uint8(0), uint8(0), true, pos)
			f.Add(n, 160, 46, 0, 2, uint8(60), uint8(30), uint8(1), false, pos)
		}
		f.Add(3, 51, 37, 0, 1, uint8(50), uint8(0), uint8(0), true, pos)
		f.Add(2, 51, 37, 1, 1, uint8(70), uint8(0), uint8(2), true, pos)
		f.Add(5, 30, 12, 0, 1, uint8(90), uint8(10), uint8(3), false, pos)
		f.Add(9, 45, 12, 0, 3, uint8(10), uint8(0), uint8(2), false, pos)
	}
	f.Fuzz(func(t *testing.T, n, w, h, top, count int, ratio, stackRatio, gap uint8, grid bool, pos string) {
		n, count = n%12, count%6
		if n < 0 {
			n = -n
		}
		// The region has to be able to hold every pane at a cell each, along
		// either axis, which is the tiler's precondition (see spans).
		w, h, top = 12+abs(w%300), 12+abs(h%120), abs(top%3)
		p := MasterParams{
			Position:   pos,
			Count:      count,
			Ratio:      float64(ratio%101) / 100,
			StackRatio: float64(stackRatio%101) / 100,
			Grid:       grid,
			Gap:        int(gap % 4),
		}
		checkMasterLayout(t, n, w, h, top, p)
	})
}

// Neighbour focus in every master position and with more than one master: a
// step always lands on a pane that touches the one it left.
func TestNeighbourOnMasterPositions(t *testing.T) {
	for _, pos := range config.MasterPositions {
		for _, count := range []int{1, 2} {
			for n := 2; n <= 7; n++ {
				t.Run(fmt.Sprintf("%s/%d masters/%d panes", pos, count, n), func(t *testing.T) {
					rects := map[int]Rect{}
					p := MasterParams{Position: pos, Count: count, Gap: 1}
					for i, l := range CalculateMasterLayout(n, 160, 46, 0, p) {
						rects[i] = Rect{l.X, l.Y, l.Width, l.Height}
					}
					checkEveryStep(t, rects, 2)
				})
			}
		}
	}
}
