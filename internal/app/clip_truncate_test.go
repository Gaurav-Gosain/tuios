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
