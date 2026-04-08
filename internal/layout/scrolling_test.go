package layout

import (
	"testing"
)

func TestScrollingLayout_AddAndRemove(t *testing.T) {
	s := NewScrollingLayout()

	s.AddColumn(1)
	s.AddColumn(2)
	s.AddColumn(3)

	if len(s.Columns) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(s.Columns))
	}
	if s.FocusedCol != 2 {
		t.Errorf("expected focused col 2, got %d", s.FocusedCol)
	}

	s.RemoveWindow(2)
	if len(s.Columns) != 2 {
		t.Fatalf("expected 2 columns after remove, got %d", len(s.Columns))
	}
}

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

func TestScrollingLayout_ApplyLayout(t *testing.T) {
	s := NewScrollingLayout()
	s.DefaultWidth = 0.5
	s.AddColumn(1)
	s.AddColumn(2)
	s.AddColumn(3)

	// Screen is 100 wide, 40 tall
	s.EnsureFocusedVisible(100)
	result := s.ComputePositions(100, 40, 2)

	// With 50% width, columns 1 and 2 should be visible (0-49, 50-99)
	// Column 3 at 100+ should be off-screen
	if len(result) < 1 {
		t.Fatalf("expected at least 1 visible window, got %d", len(result))
	}
}

func TestScrollingLayout_ConsumeExpel(t *testing.T) {
	s := NewScrollingLayout()
	s.AddColumn(1)
	s.AddColumn(2)
	s.FocusedCol = 0 // Focus first column

	s.ConsumeWindow() // Pull window 2 into column with window 1
	if len(s.Columns) != 1 {
		t.Fatalf("expected 1 column after consume, got %d", len(s.Columns))
	}
	if len(s.Columns[0].WindowIDs) != 2 {
		t.Fatalf("expected 2 windows in column, got %d", len(s.Columns[0].WindowIDs))
	}

	s.ExpelWindow() // Expel window 2 to new column
	if len(s.Columns) != 2 {
		t.Fatalf("expected 2 columns after expel, got %d", len(s.Columns))
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

func TestScrollingLayout_EnsureFocusedVisible(t *testing.T) {
	s := NewScrollingLayout()
	s.DefaultWidth = 0.5
	// Minimal scroll mode (default behavior)
	s.AddColumn(1)
	s.AddColumn(2)
	s.AddColumn(3)
	s.AddColumn(4)

	// Focus column 3 (far right)
	s.FocusedCol = 3

	// Screen 100 wide. Columns at 50% = 50 cells each.
	// Column 3 is at x=150, needs viewport at 100+ to be visible
	s.EnsureFocusedVisible(100)
	if s.ViewportX < 100 {
		t.Errorf("expected viewport >= 100, got %d", s.ViewportX)
	}
}
