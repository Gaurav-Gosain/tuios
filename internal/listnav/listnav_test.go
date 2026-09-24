package listnav

import "testing"

func TestStep(t *testing.T) {
	tests := []struct {
		name                string
		cur, delta, n, want int
		wrap                bool
	}{
		{"up from the first row wraps to the last", 0, -1, 5, 4, true},
		{"down from the last row wraps to the first", 4, 1, 5, 0, true},
		{"up from the first row stops without wrap", 0, -1, 5, 0, false},
		{"down from the last row stops without wrap", 4, 1, 5, 4, false},
		{"an inner step moves one", 2, 1, 5, 3, true},
		{"a page down past the end stops at the end", 3, 10, 5, 4, true},
		{"a page up past the start stops at the start", 1, -10, 5, 0, true},
		{"a page from the last row does not wrap", 4, 10, 5, 4, true},
		{"home", 3, Delta(Home, 10), 5, 0, true},
		{"end", 1, Delta(End, 10), 5, 4, true},
		{"an empty list answers zero", 3, 1, 0, 0, true},
		{"a one-row list stays put", 0, 1, 1, 0, true},
		{"a cursor past the end is clamped first", 9, -1, 5, 3, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Step(tt.cur, tt.delta, tt.n, tt.wrap); got != tt.want {
				t.Errorf("Step(%d, %d, %d, %v) = %d, want %d", tt.cur, tt.delta, tt.n, tt.wrap, got, tt.want)
			}
		})
	}
}

func TestStepSkip(t *testing.T) {
	// Rows 0 and 3 are headings.
	rows := []bool{false, true, true, false, true, true}
	rest := func(i int) bool { return rows[i] }
	tests := []struct {
		name             string
		cur, delta, want int
		wrap             bool
	}{
		{"down steps over a heading", 2, 1, 4, true},
		{"up steps over a heading", 4, -1, 2, true},
		{"up from the first restable row wraps to the last", 1, -1, 5, true},
		{"down from the last row wraps past the heading at the top", 5, 1, 1, true},
		{"up from the first restable row stops without wrap", 1, -1, 1, false},
		{"a page stops on the last restable row", 1, 10, 5, true},
		{"home lands on the first restable row", 5, Delta(Home, 3), 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StepSkip(tt.cur, tt.delta, len(rows), tt.wrap, rest); got != tt.want {
				t.Errorf("StepSkip(%d, %d) = %d, want %d", tt.cur, tt.delta, got, tt.want)
			}
		})
	}

	none := func(int) bool { return false }
	if got := StepSkip(2, 1, 4, true, none); got != 2 {
		t.Errorf("with nothing restable the cursor moved to %d", got)
	}
}

func TestScroll(t *testing.T) {
	tests := []struct {
		name                           string
		scroll, selected, n, vis, want int
	}{
		{"a wrap to the first row scrolls to the top", 5, 0, 10, 5, 0},
		{"a wrap to the last row scrolls to the bottom", 0, 9, 10, 5, 5},
		{"a row in view leaves the window alone", 2, 4, 10, 5, 2},
		{"a list that fits never scrolls", 3, 2, 4, 5, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Scroll(tt.scroll, tt.selected, tt.n, tt.vis); got != tt.want {
				t.Errorf("Scroll = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestKeys(t *testing.T) {
	tests := []struct {
		key     string
		letters bool
		want    Motion
	}{
		{"up", false, Up},
		{"ctrl+p", false, Up},
		{"down", false, Down},
		{"ctrl+n", false, Down},
		{"pgup", false, PageUp},
		{"pgdown", false, PageDown},
		{"home", false, Home},
		{"end", false, End},
		{"k", false, None},
		{"g", false, None},
		{"k", true, Up},
		{"j", true, Down},
		{"g", true, Home},
		{"G", true, End},
		{"x", true, None},
	}
	for _, tt := range tests {
		if got := Keys(tt.key, tt.letters); got != tt.want {
			t.Errorf("Keys(%q, %v) = %v, want %v", tt.key, tt.letters, got, tt.want)
		}
	}
}
