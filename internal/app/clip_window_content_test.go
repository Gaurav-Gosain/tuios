package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestClipWindowContent(t *testing.T) {
	tests := []struct {
		name                   string
		content                string
		x, y                   int
		viewportW, viewportH   int
		wantEmpty              bool
		wantContains           string
		wantFinalX, wantFinalY int
	}{
		{
			name:         "blank first line at origin is kept",
			content:      "\nsecond line has text\nthird line",
			viewportW:    80,
			viewportH:    24,
			wantContains: "second line",
		},
		{
			name:         "blank first line off to the right is kept",
			content:      "\nsecond line has text",
			x:            10,
			viewportW:    80,
			viewportH:    24,
			wantContains: "second line",
			wantFinalX:   10,
		},
		{
			name:      "entirely blank frame at origin is still empty",
			content:   "\n\n\n",
			viewportW: 80,
			viewportH: 24,
			wantEmpty: true,
		},
		{
			name:      "genuinely offscreen to the left is discarded",
			content:   "\nsome text here",
			x:         -40,
			viewportW: 80,
			viewportH: 24,
			wantEmpty: true,
		},
		{
			name:      "genuinely offscreen to the right is discarded",
			content:   "text",
			x:         200,
			viewportW: 80,
			viewportH: 24,
			wantEmpty: true,
		},
		{
			name:      "genuinely offscreen below is discarded",
			content:   "text",
			y:         100,
			viewportW: 80,
			viewportH: 24,
			wantEmpty: true,
		},
		{
			name:         "partially offscreen left keeps the visible part",
			content:      "\nabcdefghijklmnop",
			x:            -4,
			viewportW:    80,
			viewportH:    24,
			wantContains: "efghij",
		},
		{
			name:         "ordinary padded frame is unchanged",
			content:      "padded line one   \npadded line two   ",
			viewportW:    80,
			viewportH:    24,
			wantContains: "padded line one",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, fx, fy := clipWindowContent(tc.content, tc.x, tc.y, tc.viewportW, tc.viewportH)
			if tc.wantEmpty {
				if out != "" {
					t.Errorf("expected an empty clip, got %q", out)
				}
				return
			}
			if out == "" {
				t.Fatal("frame was discarded entirely")
			}
			if tc.wantContains != "" && !strings.Contains(out, tc.wantContains) {
				t.Errorf("clipped frame missing %q, got %q", tc.wantContains, out)
			}
			if fx != tc.wantFinalX || fy != tc.wantFinalY {
				t.Errorf("finalX,finalY = %d,%d, want %d,%d", fx, fy, tc.wantFinalX, tc.wantFinalY)
			}
		})
	}
}

// TestClipMeasuresWidestLine pins the measurement itself: a frame whose widest
// line is not its first must be clipped against the widest one, otherwise
// content that overruns the viewport is left unclipped.
func TestClipMeasuresWidestLine(t *testing.T) {
	content := "short\n" + strings.Repeat("x", 100)
	out, _, _ := clipWindowContent(content, 0, 0, 40, 24)
	for _, line := range strings.Split(out, "\n") {
		// Measure display width, not bytes: truncation appends a reset sequence.
		if w := ansi.StringWidth(line); w > 40 {
			t.Errorf("line overruns the 40 column viewport: %d columns", w)
		}
	}
}
