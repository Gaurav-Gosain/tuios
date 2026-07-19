package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestClipKeepsFrameWithBlankFirstLine is the regression test for a tiled pane
// compositing as bare background while its terminal renderer was producing
// perfectly good content.
//
// clipWindowContent measured the window width from lines[0] alone. The
// unfocused fast render path trims trailing spaces off every line, so a
// full-screen application whose top row is blank (nvim's is) produced a frame
// beginning with an empty line, which measured as zero wide. The offscreen
// guard x+windowWidth <= 0 was then true for the leftmost tile at x=0, so the
// whole frame was discarded and the layer was empty. The compositor cached that
// empty layer and served it on every later frame, so the pane stayed blank
// until refocusing rebuilt it.
//
// Verified before and after against a real binary: with the old measurement the
// layer arrives at the compositor as clipBytes=0 from boxBytes=564, and the
// pane is blank on screen.
func TestClipKeepsFrameWithBlankFirstLine(t *testing.T) {
	// 38 rows, first blank, as the unfocused fast path emits for such an app.
	var b strings.Builder
	b.WriteString("\n")
	for i := 2; i <= 18; i++ {
		b.WriteString("ALTMARKcontentZZZZZZZZ\n")
	}
	content := b.String()

	out, finalX, finalY := clipWindowContent(content, 0, 0, 120, 38)
	if out == "" {
		t.Fatal("leftmost tile discarded entirely: the pane composites as bare background")
	}
	if !strings.Contains(out, "ALTMARKcontent") {
		t.Errorf("clipped frame lost its content: %q", out)
	}
	if finalX != 0 || finalY != 0 {
		t.Errorf("finalX,finalY = %d,%d, want 0,0", finalX, finalY)
	}
}

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
