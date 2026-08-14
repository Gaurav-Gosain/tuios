package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/fuzz"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The scope of the guest-cells rule, pinned in both directions.
//
// The rule says nothing may paint into a cell a pane's guest owns. Nineteen
// fuzzer findings were one arrangement: auto-tiling off, where panes are
// free-floating windows a user may deliberately stack, so the pane in front
// owns the cell and the marker underneath it is meant to be hidden. The overlap
// rule already makes that escape for the same arrangement.
//
// The risk in adding it is that the escape swallows the bugs the rule exists
// for, so the two tests below fence it: under tiling it must never fire, and it
// must only ever excuse another pane, never the chrome.

// TestGuestCellsEscapeNeverFiresUnderTiling is the guard on the escape. Tiled
// panes partition their region, so no pane is ever in front of another one's
// cells and the rule is exactly as strong as it was before the escape existed.
func TestGuestCellsEscapeNeverFiresUnderTiling(t *testing.T) {
	for _, mode := range []string{LayoutModeBSP, LayoutModeMasterStack} {
		for _, n := range []int{2, 3, 5, 7} {
			t.Run(mode, func(t *testing.T) {
				m := gapTestOS(t, n)
				m.UseBSPLayout = true
				m.TileAllWindows()
				m.ApplyLayoutModeName(mode)
				m.TileAllWindows()

				panes := visibleFuzzPanes(m)
				for i, w := range panes {
					x, y := w.X+w.BorderOffset(), w.Y+w.BorderOffset()
					if coveredByAPaneAbove(m, panes, i, x, y, len(paneMarker(i))) {
						t.Errorf("%s at (%d,%d) is reported as covered by another pane while tiled", w.ID, x, y)
					}
				}
			})
		}
	}
}

// TestGuestCellsExcusesOnlyAPaneInFront is the guard in the other direction.
// The escape reads the pane rectangles and nothing else, so a divider, a toast,
// a tooltip or a scrollbar reaching into a pane is still a violation: none of
// them is a pane, and none of them can put a rectangle in this list.
func TestGuestCellsExcusesOnlyAPaneInFront(t *testing.T) {
	m := gapTestOS(t, 2)
	m.AutoTiling = false
	front, back := m.Windows[0], m.Windows[1]
	back.X, back.Y, back.Width, back.Height = 10, 0, 20, 10
	front.X, front.Y, front.Width, front.Height = 10, 0, 20, 10

	panes := visibleFuzzPanes(m)
	// front is later in m.Windows, so the compositor draws it over back.
	if !coveredByAPaneAbove(m, panes, 0, back.X, back.Y, 9) {
		t.Error("a pane stacked under another one is not reported as covered")
	}
	if coveredByAPaneAbove(m, panes, 1, front.X, front.Y, 9) {
		t.Error("the pane in front is reported as covered by the one behind it")
	}

	// A cell the other pane's rectangle does not reach is nobody else's, so
	// whatever is drawn there is still the rule's business.
	if coveredByAPaneAbove(m, panes, 0, back.X+30, back.Y, 9) {
		t.Error("a cell outside every other pane is reported as covered")
	}
}

// TestPaneDrawOrderMatchesTheCompositor pins the ordering the escape depends on:
// a floating pane sits above the separators, and two panes at the same depth are
// settled by the order the compositor appends their layers.
func TestPaneDrawOrderMatchesTheCompositor(t *testing.T) {
	plain := &terminal.Window{}
	later := &terminal.Window{}
	floating := &terminal.Window{IsFloating: true}
	raised := &terminal.Window{Z: 3}

	if !fuzzPaneDrawsAbove(later, 1, plain, 0) {
		t.Error("the pane appended later does not draw above the one before it")
	}
	if fuzzPaneDrawsAbove(plain, 0, later, 1) {
		t.Error("the pane appended first draws above the one after it")
	}
	if !fuzzPaneDrawsAbove(floating, 0, raised, 1) {
		t.Error("a floating pane does not draw above a raised tiled one")
	}
	if got, want := fuzzPaneZ(floating), config.ZIndexSeparators+1; got != want {
		t.Errorf("a floating pane sits at z %d, the compositor puts it at %d", got, want)
	}
}

// TestGuestCellsStillReadsAPaneThatIsOnScreen guards the off-screen escape. A
// pane the host shrank out from under has no frame cell to read, but a pane
// whose cells are on the screen must still be read even when the frame trimmed
// the blanks off its row, which is what a pane painting nothing looks like.
func TestGuestCellsStillReadsAPaneThatIsOnScreen(t *testing.T) {
	tgt, err := newFuzzTarget(fuzzScratch(t))
	if err != nil {
		t.Fatal(err)
	}
	f := tgt.(*fuzzOS)
	t.Cleanup(f.Close)
	if err := f.Reset(); err != nil {
		t.Fatal(err)
	}
	m := f.m
	w, h, n := m.GetRenderWidth(), m.GetRenderHeight(), len(paneMarker(0))
	for _, tc := range []struct {
		name     string
		x, y     int
		onScreen bool
	}{
		{"the top left cell", 0, 0, true},
		{"the last cell the marker fits in", w - n, h - 1, true},
		{"one column past the edge", w - n + 1, 0, false},
		{"one row past the bottom", 0, h, false},
		{"a rectangle the host shrank out from under", w + 15, 1, false},
		{"a negative origin", -1, 0, false},
	} {
		if got := paneCellsAreOnScreen(m, tc.x, tc.y, n); got != tc.onScreen {
			t.Errorf("%s (%d,%d): on screen=%v, want %v", tc.name, tc.x, tc.y, got, tc.onScreen)
		}
	}
}

// TestSpuriousWinchStillCatchesAnAnnouncementForNothing guards the excursion
// escape. A pane whose drawable never moved at all and was told anyway is the
// bug the rule is for, and recording excursions must not have excused it.
func TestSpuriousWinchStillCatchesAnAnnouncementForNothing(t *testing.T) {
	tgt, err := newFuzzTarget(fuzzScratch(t))
	if err != nil {
		t.Fatal(err)
	}
	f := tgt.(*fuzzOS)
	t.Cleanup(f.Close)
	if err := f.Reset(); err != nil {
		t.Fatal(err)
	}
	if vs := checkNoSpuriousResize(f); len(vs) > 0 {
		t.Fatalf("the settled fixture already reports %s", vs[0].Detail)
	}

	// An announcement with nothing behind it: no action, no size change.
	for _, rec := range f.told {
		rec.calls++
		break
	}
	vs := checkNoSpuriousResize(f)
	if len(vs) == 0 {
		t.Fatal("a pane told its size with no size change at all is not reported")
	}
	if vs[0].Rule != "spurious-winch" {
		t.Errorf("reported %s, want spurious-winch", vs[0].Rule)
	}
}

// TestOverlapEscapeTracksTheFloorTheTilersUse guards the other end of the
// layout-overlap escape: it excuses panes the tiler clamped to its floor, and
// nothing roomier than that.
func TestOverlapEscapeTracksTheFloorTheTilersUse(t *testing.T) {
	if config.DefaultWindowWidth <= config.MinWindowWidth {
		t.Fatalf("the floor the tilers clamp to (%d) is not above MinWindowWidth (%d); the escape would be pointless",
			config.DefaultWindowWidth, config.MinWindowWidth)
	}
	for _, tc := range []struct {
		name        string
		w, h        int
		wantExcused bool
	}{
		{"at the floor width", config.DefaultWindowWidth, config.DefaultWindowHeight + 10, true},
		{"at the floor height", config.DefaultWindowWidth + 10, config.DefaultWindowHeight, true},
		{"room to spare", config.DefaultWindowWidth + 10, config.DefaultWindowHeight + 10, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := gapTestOS(t, 2)
			m.AutoTiling = true
			a, b := m.Windows[0], m.Windows[1]
			a.X, a.Y, a.Width, a.Height = 0, 0, tc.w, tc.h
			b.X, b.Y, b.Width, b.Height = tc.w/2, 0, tc.w, tc.h
			a.Tiled, b.Tiled = true, true

			f := &fuzzOS{m: m}
			if got := len(checkLayoutIsDisjoint(f)) == 0; got != tc.wantExcused {
				t.Errorf("two overlapping %dx%d panes: excused=%v, want %v", tc.w, tc.h, got, tc.wantExcused)
			}
		})
	}
}

// TestGuestCellsStillHoldsForATiledSession replays the rule itself over the
// arrangement it protects, so the escape cannot have turned it off wholesale.
func TestGuestCellsStillHoldsForATiledSession(t *testing.T) {
	tgt, err := newFuzzTarget(fuzzScratch(t))
	if err != nil {
		t.Fatal(err)
	}
	f := tgt.(*fuzzOS)
	t.Cleanup(f.Close)
	if err := f.Reset(); err != nil {
		t.Fatal(err)
	}
	for _, a := range []fuzz.Action{
		{Kind: fuzz.NewPane},
		{Kind: fuzz.NewPane},
		{Kind: fuzz.Resize, A: 120, B: 40},
	} {
		if err := f.Apply(a); err != nil {
			t.Fatal(err)
		}
	}
	if !f.m.AutoTiling {
		t.Fatal("the fixture stopped being a tiled session")
	}
	if vs := checkGuestCellsAreNotPaintedOver(f); len(vs) > 0 {
		t.Errorf("a tiled session reports %s: %s", vs[0].Rule, vs[0].Detail)
	}
	panes := visibleFuzzPanes(f.m)
	if len(panes) < 4 {
		t.Fatalf("expected the fixture to leave 4 panes tiled, got %d", len(panes))
	}
	for i, w := range panes {
		if coveredByAPaneAbove(f.m, panes, i, w.X+w.BorderOffset(), w.Y+w.BorderOffset(), len(paneMarker(i))) {
			t.Errorf("%s is reported as covered while the session is tiled", w.ID)
		}
	}
}
