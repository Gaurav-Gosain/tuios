package layout

import (
	"testing"
)

func TestScrollingLayout_FocusNavigation(t *testing.T) {
	s := NewScrollingLayout()
	s.AddColumn(1)
	s.AddColumn(2)
	s.AddColumn(3)

	s.FocusLeft()
	if s.FocusedCol != 1 {
		t.Errorf("expected focused col 1 after left, got %d", s.FocusedCol)
	}

	s.FocusLeft()
	if s.FocusedCol != 0 {
		t.Errorf("expected focused col 0, got %d", s.FocusedCol)
	}

	s.FocusLeft() // Should not go below 0
	if s.FocusedCol != 0 {
		t.Errorf("expected focused col 0 (clamped), got %d", s.FocusedCol)
	}

	s.FocusRight()
	s.FocusRight()
	s.FocusRight() // Should not go past last
	if s.FocusedCol != 2 {
		t.Errorf("expected focused col 2 (clamped), got %d", s.FocusedCol)
	}
}

func TestScrollingLayout_MoveColumn(t *testing.T) {
	s := NewScrollingLayout()
	s.AddColumn(1)
	s.AddColumn(2)
	s.AddColumn(3)

	// Focus is on 3 (index 2). Move left.
	s.MoveColumnLeft()
	if s.Columns[1].WindowIDs[0] != 3 {
		t.Errorf("expected window 3 at index 1, got %d", s.Columns[1].WindowIDs[0])
	}
	if s.FocusedCol != 1 {
		t.Errorf("expected focused col 1, got %d", s.FocusedCol)
	}
}

func TestScrollingLayout_CycleWidth(t *testing.T) {
	s := NewScrollingLayout()
	s.PresetWidths = []float64{0.333, 0.5, 0.667}
	s.DefaultWidth = 0.5
	s.AddColumn(1)

	s.CycleWidth() // 0.5 -> 0.667
	if s.Columns[0].Proportion < 0.66 {
		t.Errorf("expected proportion ~0.667, got %f", s.Columns[0].Proportion)
	}

	s.CycleWidth() // 0.667 -> 0.333 (wrap)
	if s.Columns[0].Proportion > 0.34 {
		t.Errorf("expected proportion ~0.333, got %f", s.Columns[0].Proportion)
	}
}
