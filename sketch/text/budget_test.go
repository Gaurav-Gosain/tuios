package text

import (
	"math"
	"runtime"
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
// allocate, so the bound is on time. The test compares one draw of 200k
// characters with eight draws of 25k characters (checkGrowth), which does not
// depend on the speed of the machine.
func TestLongWordStaysLinear(t *testing.T) {
	if testing.Short() {
		t.Skip("draws labels of 200k characters")
	}
	const n = 200_000
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
			checkGrowth(t, tc.src, n, tc.cols)
		})
	}
}

// growth is how much longer src(n) takes to draw than src(n/8), divided by 8.
// Linear work gives about 1 and quadratic work about 8, so a bound of 3 tells
// them apart with room on both sides.
//
// A single wall-clock sample of a draw of a millisecond or two is mostly noise
// on a busy machine: one preemption or one GC cycle can double it. So each
// sample of the small input draws it 8 times, and takes about as long as one
// draw of the large input; both sizes then meet the same scheduler and timer
// granularity. The two sizes are sampled in turn after a warm-up, with a GC
// before each sample, and the fastest of five samples of each counts. Noise
// only ever adds time, so the fastest sample is the closest to the real cost.
func growth(t *testing.T, src func(n int) string, n, cols int) (ratio float64, small, large time.Duration) {
	t.Helper()
	const rounds, scale = 5, 8
	parse := func(src string) *sketch.Diagram {
		d, err := sketch.Parse(src, sketch.ParseOptions{Limit: 1 << 20})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		return d
	}
	ds, dl := parse(src(n/scale)), parse(src(n))
	// Draw errors are fine: the time is what counts.
	draw := func(d *sketch.Diagram, times int) time.Duration {
		runtime.GC()
		start := time.Now()
		for range times {
			_, _ = Draw(d, Options{Cols: cols})
		}
		return time.Since(start)
	}
	draw(ds, 1)
	draw(dl, 1)
	small, large = time.Duration(math.MaxInt64), time.Duration(math.MaxInt64)
	for range rounds {
		small = min(small, draw(ds, scale))
		large = min(large, draw(dl, 1))
	}
	return float64(large) / float64(small), small, large
}

// checkGrowth fails t when drawing src grows faster than linearly in n.
func checkGrowth(t *testing.T, src func(n int) string, n, cols int) {
	t.Helper()
	const bound = 3
	ratio, small, large := growth(t, src, n, cols)
	if ratio > bound {
		t.Errorf("8 draws of %d characters: %v, 1 draw of %d characters: %v; ratio %.1f, want at most %d (linear is about 1, quadratic about 8)", n/8, small, n, large, ratio, bound)
	}
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
			checkGrowth(t, tc.src, n, tc.cols)
		})
	}
}
