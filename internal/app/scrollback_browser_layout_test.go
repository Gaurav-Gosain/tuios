package app

import (
	"strings"
	"testing"
)

// TestALongCommandCannotTakeTheOutputsRoom. The ceiling is the other half of
// the rule: one long command must not push the output off the panel.
func TestALongCommandCannotTakeTheOutputsRoom(t *testing.T) {
	long := strings.Repeat("x", 400)
	innerW := 230

	got := browserListWidth([]string{long}, innerW)

	ceiling := innerW * browserListMaxShare / 100
	if got > ceiling {
		t.Errorf("a long command took %d of %d columns, past the ceiling of %d", got, innerW, ceiling)
	}
	if got < browserListMinWidth {
		t.Errorf("the column shrank below its floor: %d", got)
	}
}

// TestANarrowPanelKeepsTheFloor. On a narrow terminal the ceiling would fall
// below the floor, and the column has to stay usable rather than vanish.
func TestANarrowPanelKeepsTheFloor(t *testing.T) {
	if got := browserListWidth([]string{"ll"}, 30); got < 1 {
		t.Errorf("a narrow panel gave a column of %d", got)
	}
}
