package vt

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The rune ranges in which no rune carries a grapheme property that joins
// across a cluster boundary: Latin and its extensions, the symbol blocks
// from letterlike symbols to miscellaneous symbols and arrows (box drawing,
// blocks, braille, geometric shapes among them), and the private use area,
// where the Powerline and Nerd Font glyphs live.
var neverJoiningRanges = [][2]rune{
	{0x0020, 0x02FF},
	{0x2100, 0x2BFF},
	{0xE000, 0xF8FF},
}

// joinSpread is one rune of every kind that can take part in a join across
// a boundary, plus a few that cannot but sit next to ones that do.
var joinSpread = []rune{
	0x0301,  // combining acute accent (Extend)
	0x200D,  // zero width joiner
	0xFE0F,  // variation selector 16 (Extend)
	0x1F1E6, // regional indicator A
	0x1F1E9, // regional indicator D
	0x1100,  // Hangul choseong (L)
	0x1161,  // Hangul jungseong (V)
	0x11A8,  // Hangul jongseong (T)
	0xAC00,  // Hangul syllable GA (LV)
	0x0E33,  // Thai SARA AM (SpacingMark)
	0x0600,  // Arabic number sign (Prepend)
	0x1F600, // emoji
	0x4E00,  // CJK ideograph
	0x000D,  // carriage return
	0x000A,  // line feed
}

func neverJoiningCells() []string {
	var cells []string
	for _, r := range neverJoiningRanges {
		for c := r[0]; c <= r[1]; c++ {
			cells = append(cells, string(c))
		}
	}
	return cells
}

func singleASCII(s string) bool {
	return len(s) == 1 && s[0] < 0x80
}

// segmenterJoins is the answer clustersJoin must give: whether the first
// cluster of a+b reaches past a. buf is scratch space, so the full sweep
// does not allocate a string per pair.
func segmenterJoins(buf []byte, a, b string) ([]byte, bool) {
	buf = append(append(buf[:0], a...), b...)
	cl, _ := ansi.FirstGraphemeCluster(buf, ansi.GraphemeWidth)
	return buf, len(cl) > len(a)
}

// multiRuneJoinPairs is a fixed corpus of pairs in which at least one side
// is a cell of several runes: emoji ZWJ sequences, flags, VS16 tails,
// combining tails, Hangul and Thai, each set beside single runes of the
// symbol ranges and of the join spread and beside each other.
func multiRuneJoinPairs() [][2]string {
	named := [][2]string{
		{"\u2764\u200D", "\u2B50"},
		{"\u2764\u200D", "\u2764"},
		{"\u2600\u200D", "\u2B50"},
		{"\U0001F1E6\U0001F1E9", "\U0001F1E6"},
		{"\u2764\uFE0F", "\u2500"},
		{"e\u0301", "\u2500"},
		{"\uAC00", "\u11A8"},
		{"\u1100\u1161", "\u11A8"},
	}
	multi := []string{
		"\U0001F468\u200D\U0001F469\u200D\U0001F467", // family
		"\U0001F3F3\uFE0F\u200D\U0001F308",           // rainbow flag
		"\U0001F441\uFE0F\u200D\U0001F5E8\uFE0F",     // eye in speech bubble
		"\u2764\uFE0F\u200D\U0001F525",               // heart on fire
		"\U0001F9D1\u200D\U0001F4BB",                 // technologist
		"\U0001F600\u200D",                           // emoji ending in ZWJ
		"\u2B50\u200D",                               // symbol-range emoji ending in ZWJ
		"\u2500\u200D",                               // box drawing ending in ZWJ
		"\U0001F1E6\U0001F1E9",                       // flag
		"\U0001F1FA\U0001F1F8",                       // flag
		"\U0001F1EF\U0001F1F5",                       // flag
		"\u2764\uFE0F",                               // VS16 tail
		"\u263A\uFE0F",                               // VS16 tail
		"\u2194\uFE0F",                               // VS16 tail on an arrow
		"e\u0301",                                    // combining tail
		"a\u0308\u0323",                              // two combining marks
		"\u2500\u0301",                               // box drawing with a mark
		"\uE0B0\u0301",                               // private use with a mark
		"\u1100\u1161",                               // Hangul L V
		"\u1100\u1161\u11A8",                         // Hangul L V T
		"\uAC00\u11A8",                               // Hangul LV T
		"\u0E01\u0E33",                               // Thai KO KAI with SARA AM
		"\u0E01\u0E34",                               // Thai KO KAI with SARA I
		"\u0600\u0661",                               // Prepend before a digit
	}
	singles := []string{
		"a", " ", "\u00E9", "\u0250", "\u2500", "\u2588", "\u28FF", "\u2B50", "\u2764",
		"\uE0B0", "\uF8FF", "\u0301", "\u200D", "\uFE0F", "\U0001F1E6", "\U0001F1E9",
		"\u1161", "\u11A8", "\u0E33", "\U0001F600", "\u4E00",
	}
	pairs := named
	for _, m := range multi {
		for _, s := range singles {
			pairs = append(pairs, [2]string{m, s}, [2]string{s, m})
		}
		for _, n := range multi {
			pairs = append(pairs, [2]string{m, n})
		}
	}
	return pairs
}

// TestClusterJoinAgreesWithTheSegmenter holds clustersJoin to the grapheme
// segmenter on every pair of cells it could be asked about whose answer a
// shortcut might get wrong: every pair of runes from the ranges that never
// join, each of those beside every kind of rune that can, those kinds beside
// each other, and cells of several runes whose last or first rune is the one
// that joins. A pair of single ASCII bytes is answered before the segmenter
// is asked and is left out.
func TestClusterJoinAgreesWithTheSegmenter(t *testing.T) {
	cells := neverJoiningCells()
	spread := make([]string, len(joinSpread))
	for i, r := range joinSpread {
		spread[i] = string(r)
	}
	var buf []byte
	check := func(t *testing.T, a, b string) bool {
		t.Helper()
		if singleASCII(a) && singleASCII(b) {
			return true
		}
		var want bool
		buf, want = segmenterJoins(buf, a, b)
		if got := clustersJoin(a, b); got != want {
			t.Errorf("clustersJoin(%+q, %+q) = %v, the segmenter says %v", a, b, got, want)
			return false
		}
		return true
	}

	t.Run("pairs of runes that never join", func(t *testing.T) {
		n := len(cells)
		total := n * n
		stride := 1
		if testing.Short() {
			stride = 499
		}
		checked := 0
		for k := 0; k < total; k += stride {
			if !check(t, cells[k/n], cells[k%n]) {
				return
			}
			checked++
		}
		if testing.Short() && checked < 100000 {
			t.Fatalf("the short sample checked %d pairs, want at least 100000", checked)
		}
	})

	t.Run("runes that never join beside runes that can", func(t *testing.T) {
		for _, c := range cells {
			for _, s := range spread {
				if !check(t, c, s) || !check(t, s, c) {
					return
				}
			}
		}
	})

	t.Run("runes that can join beside each other", func(t *testing.T) {
		for _, a := range spread {
			for _, b := range spread {
				if !check(t, a, b) {
					return
				}
			}
		}
	})

	t.Run("cells of several runes", func(t *testing.T) {
		pairs := multiRuneJoinPairs()
		if len(pairs) < 308 {
			t.Fatalf("the corpus holds %d pairs, want at least 308", len(pairs))
		}
		for _, p := range pairs {
			if !check(t, p[0], p[1]) {
				return
			}
		}
	})
}

const (
	symbolScreenCols = 207
	symbolScreenRows = 55
)

// symbolScreen draws a full 207x55 screen of the kind a system monitor or a
// prompt theme draws: box drawing frames, block element bars, braille
// graphs, Powerline separators and ASCII labels, every row filled to the
// last column. The pen changes only its foreground, always to an indexed
// colour, and is never reset, so every change takes the palette path in
// penDiff and the cost left is the cluster check between cells.
func symbolScreen() string {
	var b strings.Builder
	b.WriteString("\x1b[2J")
	colour := 16
	pen := func() {
		colour = 16 + (colour-16+37)%216
		fmt.Fprintf(&b, "\x1b[38;5;%dm", colour)
	}
	bars := []rune("█▉▊▋▌▍▎▏░▒▓")
	braille := []rune("⣿⣷⣯⣟⡿⢿⣻⣽⣾⡀⣀⣄⣤⣦⣶")
	labels := []string{"cpu", "mem", "net", "disk", "load", "swap", "proc", "temp"}
	for y := 1; y <= symbolScreenRows; y++ {
		fmt.Fprintf(&b, "\x1b[%d;1H", y)
		cols := 0
		put := func(s string) {
			b.WriteString(s)
			cols += ansi.StringWidth(s)
		}
		switch y {
		case 1, symbolScreenRows:
			left, right := "┌", "┐"
			if y == symbolScreenRows {
				left, right = "└", "┘"
			}
			pen()
			put(left)
			for cols < symbolScreenCols-1 {
				if cols%23 == 0 {
					pen()
				}
				put("─")
			}
			pen()
			put(right)
		default:
			pen()
			put("│")
			for i := 0; cols < symbolScreenCols-1; i++ {
				pen()
				switch i % 4 {
				case 0:
					label := labels[(y+i)%len(labels)]
					if cols+len(label) > symbolScreenCols-1 {
						label = label[:symbolScreenCols-1-cols]
					}
					put(label)
				case 1:
					for j := 0; j < 9 && cols < symbolScreenCols-1; j++ {
						put(string(bars[(y+i+j)%len(bars)]))
					}
				case 2:
					for j := 0; j < 12 && cols < symbolScreenCols-1; j++ {
						put(string(braille[(y*3+i+j)%len(braille)]))
					}
				case 3:
					put("\uE0B0")
				}
			}
			pen()
			put("│")
		}
		if cols != symbolScreenCols {
			panic(fmt.Sprintf("symbol screen row %d is %d columns wide, want %d", y, cols, symbolScreenCols))
		}
	}
	return b.String()
}

// TestRenderingASymbolScreenSkipsTheSegmenter pins the cost of rendering a
// screen of box drawing, blocks, braille and Powerline glyphs. Every
// neighbouring pair of such cells used to concatenate the two cells and run
// the grapheme segmenter, an allocation per pair, although no pair of them
// can join.
func TestRenderingASymbolScreenSkipsTheSegmenter(t *testing.T) {
	e := NewEmulator(symbolScreenCols, symbolScreenRows)
	e.WriteString(symbolScreen())
	if got := testing.AllocsPerRun(20, func() { _ = e.Render() }); got > 400 {
		t.Errorf("rendering a %dx%d symbol screen allocates %.0f times, want at most 400", symbolScreenCols, symbolScreenRows, got)
	}
}

// TestClusterJoinAgreesWithTheSegmenterAtRangeEdges holds clustersJoin to
// the segmenter on the runes just outside the ranges that never join, where
// combining blocks that do join sit next to them, each beside every rune of
// those ranges in both orders. It also holds it to the segmenter on cells
// whose first rune joins the cell before them while their last rune is one
// that never joins.
func TestClusterJoinAgreesWithTheSegmenterAtRangeEdges(t *testing.T) {
	edges := []rune{
		0x0300, // combining grave accent, just past the Latin range
		0x0301, // combining acute accent
		0x036F, // combining latin small letter x, end of the block
		0x20D0, // combining left harpoon above, start of the symbol marks
		0x20E3, // combining enclosing keycap
		0x20F0, // combining asterisk above, end of the symbol marks
		0x2C00, // Glagolitic, just past the symbol range
		0xF900, // CJK compatibility ideograph, just past the private use area
	}
	var buf []byte
	check := func(t *testing.T, a, b string) bool {
		t.Helper()
		var want bool
		buf, want = segmenterJoins(buf, a, b)
		if got := clustersJoin(a, b); got != want {
			t.Errorf("clustersJoin(%+q, %+q) = %v, the segmenter says %v", a, b, got, want)
			return false
		}
		return true
	}

	t.Run("runes just outside the ranges", func(t *testing.T) {
		for _, c := range neverJoiningCells() {
			for _, r := range edges {
				e := string(r)
				if !check(t, c, e) || !check(t, e, c) {
					return
				}
			}
		}
	})

	t.Run("cells that join at their first rune only", func(t *testing.T) {
		pairs := [][2]string{
			{"\u2500", "\u0301\u2500"},
			{" ", "\u0300a"},
			{"a", "\u20D0\u2500"},
			{"\u2764", "\u200D\u2764"},
			{"\u2500", "\uFE0F\u2588"},
			{"\u00E9", "\u0308\uE0B0"},
			{"\uE0B0", "\u20E3 "},
		}
		for _, p := range pairs {
			if !check(t, p[0], p[1]) {
				return
			}
		}
	})
}
