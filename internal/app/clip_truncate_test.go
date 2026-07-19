package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestClipNeverExceedsViewportWidth checks the contract the compositor relies
// on: a clipped row is never wider than the space the layer was given.
//
// ansi.Truncate and ansi.StringWidth disagree about malformed UTF-8, so a line
// carrying invalid bytes came back from the truncate one cell over the limit
// and bled into the neighbouring pane. Guest programs can put arbitrary bytes
// into an OSC title and those titles are rendered into the window chrome, so
// this is reachable from any program the user runs.
func TestClipNeverExceedsViewportWidth(t *testing.T) {
	lines := []string{
		// The shape the fuzzer found: wide characters followed by a truncated
		// multi-byte sequence.
		"世世世世世\xe4\xb800\xb80",
		"\xe4\xb8",
		"ab\xffcd\xfe",
		"世\xe4\xb8世\xb8世",
		strings.Repeat("\xe4\xb8", 20),
		// Valid content must keep working too.
		"世世世世世世世世",
		"a世b世c世d",
		strings.Repeat("x", 200),
	}

	// Sweep the placement and the viewport width together. The overflow only
	// appears when the space left for the row lands exactly on the cell where
	// the two measurements disagree, so a single geometry proves nothing.
	const viewportHeight = 24

	for _, line := range lines {
		for viewportWidth := 1; viewportWidth <= 96; viewportWidth++ {
			for _, x := range []int{0, 1, 40, 70, 78, 79, -5, -20} {
				if x >= viewportWidth {
					continue
				}
				got, finalX, _ := clipWindowContent(line, x, 0, viewportWidth, viewportHeight)
				if got == "" {
					continue
				}
				maxCols := viewportWidth - finalX
				for i, row := range strings.Split(got, "\n") {
					if w := ansi.StringWidth(row); w > maxCols {
						t.Fatalf("line %q at x=%d in a %d-wide viewport: "+
							"row %d is %d cells, viewport offered %d",
							line, x, viewportWidth, i, w, maxCols)
					}
				}
			}
		}
	}
}

// TestTruncateToWidth checks the helper directly, including that it does not
// over-trim content that already fits.
func TestTruncateToWidth(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		width int
	}{
		{"invalid utf8 at boundary", "世世世世世\xe4\xb800\xb80", 11},
		{"invalid utf8 short", "\xe4\xb8ab", 2},
		{"valid wide straddling", "世世世世世世", 11},
		{"valid wide exact", "世世世世世世", 12},
		{"narrow", "abcdef", 3},
		{"already fits", "abc", 10},
		{"zero width", "abc", 0},
		{"styled", "\x1b[31m世世世\x1b[0m", 5},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateToWidth(tc.line, tc.width)
			if w := ansi.StringWidth(got); w > tc.width {
				t.Errorf("truncateToWidth(%q, %d) = %q, width %d",
					tc.line, tc.width, got, w)
			}
			// It must not throw away content that fit: the result is at least
			// as wide as ansi.Truncate's, minus the correction it needed.
			if tc.width > 0 && ansi.StringWidth(tc.line) <= tc.width && got != tc.line {
				t.Errorf("truncateToWidth trimmed a line that already fit: %q -> %q",
					tc.line, got)
			}
		})
	}
}
