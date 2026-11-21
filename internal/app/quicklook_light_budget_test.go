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
		m.quickLook.framed = quickLookFrame{}
		start := time.Now()
		content, _, _ = m.renderQuickLook()
		best = min(best, time.Since(start))
	}
	t.Logf("fastest of 5 draws of a %d-byte line searched for %q: %v", len(line), "a", best)
	if best > ceiling {
		t.Errorf("drawing a wrapped %d-byte line searched for %q took %v at best, want at most %v", len(line), "a", best, ceiling)
	}

	flat := func(c color.Color) string {
		r, g, b, _ := c.RGBA()
		return fmt.Sprintf("48;2;%d;%d;%d", r>>8, g>>8, b>>8)
	}
	l, _ := m.quickLookLook(m.overlayUI())
	accent := flat(l.pal.Accent)
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

	cases := []struct {
		name, line, q string
		codeW, k      int
		want          [][2]int
	}{
		{
			name: "conjunct cut by the query", line: "ab \u0915\u094d\u0937 cd", q: "\u0937",
			codeW: 16, k: 0, want: [][2]int{{5, 0}},
		},
		{
			name: "after a conjunct", line: "ab \u0915\u094d\u0937 cd", q: "cd",
			codeW: 16, k: 0, want: [][2]int{{6, 0}, {7, 0}},
		},
		{
			name: "conjunct cut by the query on a wrapped row", line: "abcdefgh \u0915\u094d\u0937 cdx", q: "\u0937",
			codeW: 4, k: 2, want: [][2]int{{3, 0}},
		},
		{
			name: "after a conjunct on a wrapped row", line: "abcdefgh \u0915\u094d\u0937 cdx", q: "cd",
			codeW: 4, k: 2, want: [][2]int{{4, 0}},
		},
		{
			name: "after jamo", line: "ab \u1100\u1161\u11a8 cd", q: "cd",
			codeW: 16, k: 0, want: [][2]int{{7, 0}, {8, 0}},
		},
		{
			name: "after jamo on a wrapped row", line: "abcdefg \u1100\u1161\u11a8cd", q: "cd",
			codeW: 4, k: 2, want: [][2]int{{3, 0}, {4, 0}},
		},
		{
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
