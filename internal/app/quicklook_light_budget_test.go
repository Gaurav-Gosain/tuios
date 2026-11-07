package app

import (
	"fmt"
	"image/color"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/diffview"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// TestQuickLookLightsMatchesInLinearTime holds the draw of a searched long
// line to a budget. With wrap on, every row of the line lights the matches
// it shows, and each scroll step or typed character draws the panel again.
// A held line is cut at quickLookLineMax bytes before ckCodeText turns each
// tab into four spaces, so it can reach 256 KiB. The line here, 64 KiB of
// one letter searched for that letter, is a heavy case with a match at
// every byte, not the most matches a held line can hold.
//
// The budget is wall time: no count stands in for this CPU. It is the
// fastest of 5 runs, because load only adds time, and it is skipped under
// -race and -short. On go1.27.3 at 200x60 the draw took 2.2 to 3.1 s
// before the matches were lit in one walk along the line, and about 70 ms
// after. The ceiling of 300 ms is about 4x above the second and 7x under the
// first, so a shared runner can slow the fixed code a few times without
// reaching it.
//
// The matches must still be lit: after the timed runs, the drawn panel is
// read back into cells, and a code row carries the accent ground on every
// letter it shows.
func TestQuickLookLightsMatchesInLinearTime(t *testing.T) {
	if testing.Short() {
		t.Skip("timing budget")
	}
	if raceEnabled {
		t.Skip("timings are not meaningful under -race")
	}
	was := theme.ColorProfile()
	theme.SetColorProfile(colorprofile.TrueColor)
	t.Cleanup(func() { theme.SetColorProfile(was) })

	m := keystrokeOS(t, 1, 200, 60)
	m.KeybindRegistry = config.NewKeybindRegistry(config.DefaultConfig())
	line := strings.Repeat("a", quickLookLineMax)
	target := quickLookTarget{path: "/src/letters.txt", name: "letters.txt"}
	m.quickLook = quickLookState{open: true, target: target, wrap: true, query: "a", res: &quickLookResult{
		target: target, kind: "text", size: int64(len(line)), lines: []string{line},
	}}

	const ceiling = 300 * time.Millisecond
	var content string
	best := time.Duration(1<<63 - 1)
	for range 5 {
		// The panel memo would serve every run after the first.
		m.quickLook.framed = quickLookFrame{}
		start := time.Now()
		content, _, _ = m.renderQuickLook()
		best = min(best, time.Since(start))
	}
	t.Logf("fastest of 5 draws of a %d-byte line searched for %q: %v", len(line), "a", best)
	if best > ceiling {
		t.Errorf("drawing a wrapped %d-byte line searched for %q took %v at best, want at most %v", len(line), "a", best, ceiling)
	}

	// The accent as the parameters of a true colour ground.
	flat := func(c color.Color) string {
		r, g, b, _ := c.RGBA()
		return fmt.Sprintf("48;2;%d;%d;%d", r>>8, g>>8, b>>8)
	}
	l, _ := m.quickLookLook(m.overlayUI())
	accent := flat(l.pal.Accent)
	// The line is all one letter, so the rows with the most letters are the
	// code rows, and every letter on them is a match.
	widest, lit := 0, false
	for row := range strings.SplitSeq(content, "\n") {
		w := ansi.StringWidth(row)
		if w == 0 {
			continue
		}
		buf := uv.NewScreenBuffer(w, 1)
		buf.Method = ansi.GraphemeWidth
		uv.NewStyledString(row).Draw(buf, uv.Rect(0, 0, w, 1))
		letters, onAccent := 0, 0
		for x := range w {
			cell := buf.CellAt(x, 0)
			if cell == nil || cell.Content != "a" {
				continue
			}
			letters++
			if cell.Style.Bg != nil && flat(cell.Style.Bg) == accent {
				onAccent++
			}
		}
		switch {
		case letters > widest:
			widest, lit = letters, onAccent == letters
		case letters == widest && onAccent == letters:
			lit = true
		}
	}
	if widest == 0 {
		t.Fatalf("the panel shows none of the line:\n%s", ansi.Strip(content))
	}
	if !lit {
		t.Errorf("no code row of the panel is lit across its %d cells for the search %q (ground %s):\n%s",
			widest, "a", accent, ansi.Strip(content))
	}
}

// TestQuickLookLightsTheBaseCells pins the cells a search lights on rows
// that hold a grapheme cluster whose code points add up to more cells than
// the cluster takes. The panel and the end-to-end tests search ASCII only,
// and no end-to-end test reaches these columns, so they are held here, on
// one row drawn the way the panel draws it and then lit.
//
// Every cell listed is the column ansi.StringWidth gives for the text before
// the match, less the row's offset into the line, after codeX 1.
func TestQuickLookLightsTheBaseCells(t *testing.T) {
	was := theme.ColorProfile()
	theme.SetColorProfile(colorprofile.TrueColor)
	t.Cleanup(func() { theme.SetColorProfile(was) })

	m := keystrokeOS(t, 1, 200, 60)
	l, dv := m.quickLookLook(m.overlayUI())
	pal := l.pal
	const codeX, y = 1, 0
	// lightRow draws row k of line with wrap on, lights q on it, and returns
	// the lit cells.
	lightRow := func(line, q string, codeW, k int) [][2]int {
		c := (*ckCanvas)(nil).reset(codeX+codeW+2, 1, ckColor(pal.Canvas))
		c.draw(codeX, y, codeW, dv.Code(line, nil, diffview.Range{}, diffview.Context, false, k*codeW, codeW+1))
		quickLookLightMatches(c, pal, q, line, codeX, y, codeW, k*codeW)
		var got [][2]int
		for x := range c.w {
			if cell := c.buf.CellAt(x, y); cell != nil && cell.Style.Bg == pal.Accent {
				got = append(got, [2]int{x, y})
			}
		}
		return got
	}

	// U+0915 U+094D U+0937 is the conjunct ksha: one cell, its code points
	// two. U+1100 U+1161 U+11A8 is the Hangul syllable gak written as jamo:
	// two cells, its code points four.
	cases := []struct {
		name, line, q string
		codeW, k      int
		want          [][2]int
	}{
		{
			// "ab " is cells 0-2, the conjunct cell 3, " " cell 4. The
			// query starts at byte 9, inside the conjunct: "ab क्"
			// measures 4, so x = 1+4 = 5, the space after the conjunct
			// (x 4). That is where the base lights it, not a claim that it
			// is right.
			name: "conjunct cut by the query", line: "ab \u0915\u094d\u0937 cd", q: "\u0937",
			codeW: 16, k: 0, want: [][2]int{{5, 0}},
		},
		{
			// "ab क्ष " measures 3+1+1 = 5, so "cd" is
			// x = 1+5, 1+6.
			name: "after a conjunct", line: "ab \u0915\u094d\u0937 cd", q: "cd",
			codeW: 16, k: 0, want: [][2]int{{6, 0}, {7, 0}},
		},
		{
			// Row 2 of 4 cells starts at column 8. The query starts at
			// byte 15, inside the conjunct: "abcdefgh क्" measures
			// 8+1+1 = 10, 10-8 = 2, x = 1+2 = 3. Where the base lights it,
			// as above.
			name: "conjunct cut by the query on a wrapped row", line: "abcdefgh \u0915\u094d\u0937 cdx", q: "\u0937",
			codeW: 4, k: 2, want: [][2]int{{3, 0}},
		},
		{
			// The "cd" at bytes 2-3 is columns 2-3, 2-8 < 0: off this row,
			// nothing lit. The one at byte 19: "abcdefgh क्ष "
			// measures 8+1+1+1 = 11, 11-8 = 3, x = 1+3 = 4; its d is column
			// 12, past the row's 4 cells.
			name: "after a conjunct on a wrapped row", line: "abcdefgh \u0915\u094d\u0937 cdx", q: "cd",
			codeW: 4, k: 2, want: [][2]int{{4, 0}},
		},
		{
			// "ab " is cells 0-2, the syllable cells 3-4, " " cell 5, so
			// "cd" is x = 1+6, 1+7.
			name: "after jamo", line: "ab \u1100\u1161\u11a8 cd", q: "cd",
			codeW: 16, k: 0, want: [][2]int{{7, 0}, {8, 0}},
		},
		{
			// The "cd" at bytes 2-3 is columns 2-3, 2-8 < 0: nothing lit.
			// The one at byte 17: "abcdefg 각" measures
			// 7+1+2 = 10, 10-8 = 2, x = 1+2, 1+3.
			name: "after jamo on a wrapped row", line: "abcdefg \u1100\u1161\u11a8cd", q: "cd",
			codeW: 4, k: 2, want: [][2]int{{3, 0}, {4, 0}},
		},
		{
			// U+212A is the Kelvin sign, three bytes, which lowers to the
			// one byte k, so the lowered copy "aak丞" is two bytes
			// shorter and finds U+4E1E at byte 3, inside the Kelvin sign
			// of the line. The text before it, "aa" and the first byte of
			// the sign cut there, measures 2, so x = 1+2 = 3. That is where
			// the base lights a match on a line whose lowered copy is
			// shorter, not a claim that it is right.
			name: "match found inside a rune of a line that lowers shorter", line: "aa\u212a\u4e1e", q: "\u4e1e",
			codeW: 16, k: 0, want: [][2]int{{3, 0}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := lightRow(tc.line, tc.q, tc.codeW, tc.k); !slices.Equal(got, tc.want) {
				t.Errorf("row %d of %q (%d cells) searched for %q lights %v, want %v", tc.k, tc.line, tc.codeW, tc.q, got, tc.want)
			}
		})
	}
}
