package vis

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The assertions the capture round runs. They are made on the drawn frame
// because that is where the display's two real bugs lived: state that was
// correct, drawn wrong.

// assertRectangular is the figure/ground claim. Every row has to be exactly as
// wide as the frame, or the missing cells show whatever the terminal had there
// before and the harness reads as a hole in the app.
func assertRectangular(t *testing.T, frame string, w, h int) {
	t.Helper()
	lines := strings.Split(frame, "\n")
	if len(lines) != h {
		t.Errorf("frame is %d rows, want %d", len(lines), h)
	}
	for i, l := range lines {
		if got := lipgloss.Width(l); got != w {
			t.Errorf("row %d is %d cells, want %d: %q", i, got, w, ansi.Strip(l))
		}
	}
}

// assertASCII is the degradation gate. It found a real leak in the end card
// during the design's own capture round, where an arrow and a multiplication
// sign survived into a frame that had asked for ASCII.
func assertASCII(t *testing.T, frame string) {
	t.Helper()
	for i, r := range frame {
		if r > 127 {
			t.Fatalf("ASCII frame carries %q (U+%04X) at byte %d", r, r, i)
		}
	}
}
