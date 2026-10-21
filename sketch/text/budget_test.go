package text

import (
	"strconv"
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

// TestLongLabelsStayLinear draws the labels of the newer types at sketch's
// default source limit. cut trimmed a label one rune at a time and measured it
// again each time, and wrapCells measured its line and the rest of a cut word
// again each time: 7 s and 1.3 GB for a 50,000-character radar axis, treemap or
// Wardley label at 80 columns. Railroad choices were quadratic the same way. The bound is the same ratio as above, at the default
// limit, so a regression stays within a few seconds and 1.5 GB.
func TestLongLabelsStayLinear(t *testing.T) {
	if testing.Short() {
		t.Skip("draws labels of 50k characters")
	}
	const n = 50_000
	const ratio = 24
	word := func(n int) string { return strings.Repeat("x", n) }
	words := func(n int) string { return strings.Repeat("word ", n/5) }
	for name, tc := range map[string]struct {
		src  func(n int) string
		cols int
	}{
		"radar axis":        {func(n int) string { return "radar-beta\n axis a[\"" + words(n) + "\"], b, c\n curve c{1,2,3}" }, 80},
		"treemap label":     {func(n int) string { return "treemap-beta\n\"" + word(n) + "\": 5" }, 80},
		"wardley component": {func(n int) string { return "wardley-beta\ncomponent " + word(n) + " [0.5, 0.5]" }, 0},
		"cynefin item":      {func(n int) string { return "cynefin-beta\n complex\n  \"" + words(n) + "\"" }, 0},
		"event model frame": {func(n int) string { return "eventmodeling\ntf 01 ui " + word(n) }, 80},
		"railroad choices": {func(n int) string {
			alts := make([]string, n/6)
			for i := range alts {
				alts[i] = "\"" + strconv.Itoa(i) + "\""
			}
			return "railroad-ebnf-beta\nr = " + strings.Join(alts, " | ") + " ;"
		}, 80},
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
