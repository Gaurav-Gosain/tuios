package layout

import (
	"testing"
)

// arranged lays out windows 1..n as kind and returns their rectangles.
func arranged(t *testing.T, kind string, n int, bounds Rect) map[int]Rect {
	t.Helper()
	tree := NewBSPTree()
	ids := make([]int, n)
	for i := range ids {
		ids[i] = i + 1
	}
	if err := tree.ArrangeTree(kind, ids); err != nil {
		t.Fatal(err)
	}
	if tree.WindowCount() != n {
		t.Fatalf("%s: tree holds %d windows, want %d", kind, tree.WindowCount(), n)
	}
	return tree.ApplyLayout(bounds, 0)
}

func TestArrangeEvenHorizontalIsEven(t *testing.T) {
	got := arranged(t, ArrangeEvenHorizontal, 4, Rect{W: 120, H: 40})
	for id := 1; id <= 4; id++ {
		r := got[id]
		if r.W != 30 || r.H != 40 || r.X != (id-1)*30 {
			t.Errorf("window %d = %+v, want x=%d w=30 h=40", id, r, (id-1)*30)
		}
	}
}

func TestArrangeEvenVerticalIsEven(t *testing.T) {
	got := arranged(t, ArrangeEvenVertical, 3, Rect{W: 90, H: 30})
	for id := 1; id <= 3; id++ {
		r := got[id]
		if r.H != 10 || r.W != 90 || r.Y != (id-1)*10 {
			t.Errorf("window %d = %+v, want y=%d h=10 w=90", id, r, (id-1)*10)
		}
	}
}

// Five panes tile as a row of three over a row of two, as tmux does.
func TestArrangeTiledIsAGrid(t *testing.T) {
	got := arranged(t, ArrangeTiled, 5, Rect{W: 120, H: 40})
	for id := 1; id <= 3; id++ {
		if r := got[id]; r.Y != 0 || r.H != 20 || r.W != 40 {
			t.Errorf("top row window %d = %+v", id, r)
		}
	}
	for id := 4; id <= 5; id++ {
		if r := got[id]; r.Y != 20 || r.H != 20 || r.W != 60 {
			t.Errorf("bottom row window %d = %+v", id, r)
		}
	}
	if one := arranged(t, ArrangeTiled, 1, Rect{W: 10, H: 10}); one[1] != (Rect{W: 10, H: 10}) {
		t.Errorf("one pane = %+v", one[1])
	}
}

func TestArrangeRejectsAnUnknownLayout(t *testing.T) {
	if err := NewBSPTree().ArrangeTree("main-vertical", []int{1}); err == nil {
		t.Fatal("an unknown layout was accepted")
	}
}
