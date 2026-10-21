package text

import (
	"strings"
	"testing"
	"time"

	"tuios.dev/sketch"
)

// TestLongWordStaysLinear draws labels that are one word of 200,000
// characters. wrap cuts such a word into many lines. When it measured the rest
// of the word again after each cut, the cost grew with the square of the
// length: 2.7 s for a quadrant axis label of 200k characters. Cutting does not
// allocate, so the bound is on time. The test compares the time for 200k
// characters with the time for 25k characters, which does not depend on the
// speed of the machine. Linear work takes about 8 times as long. Quadratic work
// takes about 64 times as long.
func TestLongWordStaysLinear(t *testing.T) {
	if testing.Short() {
		t.Skip("draws labels of 200k characters")
	}
	const n = 200_000
	const ratio = 24
	for name, tc := range map[string]struct {
		src  func(n int) string
		cols int
	}{
		"xychart title": {
			src: func(n int) string {
				return "xychart\n title " + strings.Repeat("a", n) + "\n x-axis [a, b]\n bar [1, 2]"
			},
			cols: 80,
		},
		"quadrant y-axis label": {
			src: func(n int) string {
				return "quadrantChart\n y-axis " + strings.Repeat("a", n) + " --> b\n p: [0.5, 0.5]"
			},
			cols: 80,
		},
		"quadrant x-axis label": {
			src: func(n int) string {
				return "quadrantChart\n x-axis " + strings.Repeat("a", n) + " --> b\n p: [0.5, 0.5]"
			},
			cols: 80,
		},
	} {
		t.Run(name, func(t *testing.T) {
			small := drawTime(t, tc.src(n/8), tc.cols)
			large := drawTime(t, tc.src(n), tc.cols)
			if large > small*ratio {
				t.Errorf("%d characters: %v, %d characters: %v; want at most %d times as long", n/8, small, n, large, ratio)
			}
		})
	}
}

// drawTime is the fastest of three draws of src. Draw errors are fine: the
// time is what counts.
func drawTime(t *testing.T, src string, cols int) time.Duration {
	t.Helper()
	d, err := sketch.Parse(src, sketch.ParseOptions{Limit: 1 << 20})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	best := time.Duration(1<<63 - 1)
	for range 3 {
		start := time.Now()
		_, _ = Draw(d, Options{Cols: cols})
		best = min(best, time.Since(start))
	}
	return max(best, time.Millisecond)
}
