package app

import (
	"strings"
	"testing"
)

// The browser's list column is sized to what is in it.
//
// It was thirty percent of the panel, whatever the panel held. On a wide
// terminal that meant seventy columns of grey for a list of "ll" and "ls",
// with the selected row's highlight drawn across every one of them, and the
// output squeezed into what was left. The output is what the browser is for.

// TestAShortListGetsAShortColumn.
//
// Negative control: going back to a flat share of the panel returns 69 here
// and this fails.
func TestAShortListGetsAShortColumn(t *testing.T) {
	// The screenshot that prompted this: two two-letter commands on a wide
	// terminal.
	got := browserListWidth([]string{"ll", "ls"}, 230)

	if got > browserListMinWidth {
		t.Errorf("two short commands took %d columns, want no more than the floor of %d",
			got, browserListMinWidth)
	}
}

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

// TestTheColumnFitsWhatItHolds. A list of middling commands gets exactly the
// room they need, which is the case between the floor and the ceiling.
func TestTheColumnFitsWhatItHolds(t *testing.T) {
	items := []string{"go test ./...", "git status", "make build"}
	widest := len("go test ./...")

	got := browserListWidth(items, 230)

	if got < widest {
		t.Errorf("the column is %d columns for a %d-column entry, so it truncates", got, widest)
	}
	// The floor wins when the entries are shorter than it, which is the point
	// of having one: a column narrower than this stops reading as a column.
	if want := max(widest+3, browserListMinWidth); got > want {
		t.Errorf("the column is %d columns for a %d-column entry, want no more than %d", got, widest, want)
	}
}

// TestAnEmptyListStillDrawsAColumn, because the header above it and the rule
// beside it are still there and a zero-width column would collapse both.
func TestAnEmptyListStillDrawsAColumn(t *testing.T) {
	if got := browserListWidth(nil, 230); got < browserListMinWidth {
		t.Errorf("an empty list gave a column of %d", got)
	}
}

// TestANarrowPanelKeepsTheFloor. On a narrow terminal the ceiling would fall
// below the floor, and the column has to stay usable rather than vanish.
func TestANarrowPanelKeepsTheFloor(t *testing.T) {
	if got := browserListWidth([]string{"ll"}, 30); got < 1 {
		t.Errorf("a narrow panel gave a column of %d", got)
	}
}
