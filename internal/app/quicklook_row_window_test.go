package app

import (
	"fmt"
	"image/color"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/diffview"
)

// TestQuickLookRowWindowBytes holds a quick look step on a long line to a
// budget of bytes allocated per frame. Each drawn row handed the whole line
// to the code drawer, which styled it and walked it to the end to cut one
// row of cells, so a step on a 64 KiB line allocated megabytes and took tens
// of milliseconds. Bytes are a count, steady on a loaded machine where a
// clock is not. A small file must cost no more than it did.
func TestQuickLookRowWindowBytes(t *testing.T) {
	// Under -race sync.Pool drops a share of what is put back, so the
	// count moves from run to run.
	skipAllocCountsUnderRace(t)
	m := keystrokeOS(t, 1, 200, 60)
	m.KeybindRegistry = config.NewKeybindRegistry(config.DefaultConfig())

	// perFrame opens the text, steps one line down with j, and returns the
	// median of the bytes each of 21 frames of that view allocates, drawn
	// again from nothing each time: the panel keeps a frame where nothing
	// changed, and every step changes the scroll. The bytes of one frame
	// move in steps of a few KiB with what else the process does; the
	// median does not follow one such step.
	perFrame := func(t *testing.T, name string, wrap bool, lines []string, spans [][]diffview.Span) uint64 {
		t.Helper()
		target := quickLookTarget{path: "/src/" + name, name: name}
		m.quickLook = quickLookState{open: true, wrap: wrap, target: target, res: &quickLookResult{
			target: target, kind: "text", size: int64(len(strings.Join(lines, "\n"))), lines: lines, spans: spans,
		}}
		_, _, _ = m.renderQuickLook()
		if !m.QuickLookLine(true) || m.quickLook.scroll != 1 {
			t.Fatalf("j did not move the text one line down: scroll %d", m.quickLook.scroll)
		}
		m.quickLook.framed = quickLookFrame{}
		_, _, _ = m.renderQuickLook()
		got := make([]uint64, 21)
		var before, after runtime.MemStats
		for i := range got {
			m.quickLook.framed = quickLookFrame{}
			runtime.ReadMemStats(&before)
			_, _, _ = m.renderQuickLook()
			runtime.ReadMemStats(&after)
			got[i] = after.TotalAlloc - before.TotalAlloc
		}
		slices.Sort(got)
		return got[len(got)/2]
	}
	long := strings.Repeat("x", 64<<10)

	// Measured at 82e534578 with go1.27.3: a median of 6,590 KiB a frame
	// (6,589 to 6,658 over runs). Drawing each row from its window of the
	// line is expected near 0.7 MiB.
	t.Run("wrap on, one 64 KiB line", func(t *testing.T) {
		const budget = 2 << 20
		if n := perFrame(t, "notes.txt", true, []string{"short", long}, nil); n > budget {
			t.Errorf("a step onto a 64 KiB wrapped line allocated %d KiB a frame, want at most %d KiB", n>>10, budget>>10)
		}
	})

	// Measured at 82e534578 with go1.27.3: a median of 3,901 KiB a frame,
	// every drawn row a different long line. Expected near 0.7 MiB from the
	// windows; a cut line drawn from the whole line again stays near 3.9
	// MiB, well over the budget.
	t.Run("wrap off, 60 lines of 64 KiB", func(t *testing.T) {
		const budget = 3 << 19
		lines := make([]string, 60)
		for i := range lines {
			lines[i] = long
		}
		if n := perFrame(t, "data.log", false, lines, nil); n > budget {
			t.Errorf("a step through 60 cut lines of 64 KiB allocated %d KiB a frame, want at most %d KiB", n>>10, budget>>10)
		}
	})

	// Every line here is drawn whole, as before: the longest, 300 bytes, is
	// at most max(128, 3*(107+1)) = 324 bytes, 107 being the row's width with
	// wrap off. So this guards what a frame adds around the rows, such as a
	// buffer made for every frame or row. Measured at 82e534578 with
	// go1.27.3: a median of 811 KiB a frame, held to that plus 16 KiB. At
	// the base the median read 811 KiB in each of 50 runs of one process
	// (-count=50) and in each of 10 fresh processes. Time on small files is
	// not seen here.
	t.Run("small file, short and highlighted lines", func(t *testing.T) {
		const budget = (811 + 16) << 10
		lines := make([]string, 2000)
		for i := range lines {
			lines[i] = fmt.Sprintf("var v%04d = %q // line %d", i+1, "row", i+1)
		}
		for i, n := range []int{129, 170, 210, 255, 300} {
			lines[3+i] = `var w = "` + strings.Repeat("ab", (n-10)/2) + strings.Repeat("c", (n-10)%2) + `"`
		}
		if n := perFrame(t, "main.go", false, lines, diffview.Highlight("main.go", lines)); n > budget {
			t.Errorf("a step through a small Go file allocated %d KiB a frame, want at most %d KiB", n>>10, budget>>10)
		}
	})
}

// TestQuickLookRowWindowDrawsTheSame pins the text rows quick look draws for
// lines long enough to be drawn from a window, and for short ones, in both
// wrap modes: where a row is cut, where a wide character on a row's edge
// goes, where a cut line shows its ellipsis, and that a highlighted run
// keeps its colour on the next row.
//
// A line is drawn whole when it is at most max(128, 3*(width+1)) bytes,
// width being the cells handed to the code drawer: the code column and one
// more with wrap on, the code column with wrap off. A 200 by 60 screen gives
// the panel 110 cells and a code column of 107, so 327 bytes with wrap on
// and 324 with wrap off; a 60 by 30 one gives 48 and 45, so 141 bytes with
// wrap on. The long lines below run past the rows they test by more than
// 3*(width+1)+16 cells, so those rows end before the line does.
func TestQuickLookRowWindowDrawsTheSame(t *testing.T) {
	alpha := "abcdefghijklmnopqrstuvwxyz"
	digits := "0123456789"
	// han is ten wide characters, two cells and three bytes each.
	han := "〇一二三四五六七八九"

	// draw lays the panel out on a cols by rows screen and returns the text
	// of each body row, from the first code column, with the blanks at its
	// end dropped, and the canvas. The second cell of a wide character adds
	// nothing to the text.
	draw := func(t *testing.T, cols, rows int, name string, wrap bool, lines []string, spans [][]diffview.Span) ([]string, *ckCanvas) {
		t.Helper()
		m := keystrokeOS(t, 1, cols, rows)
		m.KeybindRegistry = config.NewKeybindRegistry(config.DefaultConfig())
		target := quickLookTarget{path: "/src/" + name, name: name}
		m.quickLook = quickLookState{open: true, wrap: wrap, target: target, res: &quickLookResult{
			target: target, kind: "text", size: int64(len(strings.Join(lines, "\n"))), lines: lines, spans: spans,
		}}
		pal := m.overlayUI()
		innerW, bodyH := m.quickLookSize()
		l, dv := m.quickLookLook(pal)
		c := (*ckCanvas)(nil).reset(innerW, bodyH, ckColor(pal.Canvas))
		m.drawQuickLook(c, l, dv, innerW, bodyH)
		var out []string
		for y := 1; y < bodyH; y++ {
			var b strings.Builder
			for x := 1; x < innerW; x++ {
				if cell := c.buf.CellAt(x, y); cell != nil && (cell.Width > 0 || cell.Content != "") {
					b.WriteString(cell.Content)
				}
			}
			out = append(out, strings.TrimRight(b.String(), " "))
		}
		return out, c
	}
	// rowsAre compares the body rows drawn at the given indexes with want;
	// an empty want is a blank row.
	rowsAre := func(t *testing.T, got []string, want map[int]string) {
		t.Helper()
		idx := make([]int, 0, len(want))
		for i := range want {
			idx = append(idx, i)
		}
		slices.Sort(idx)
		for _, i := range idx {
			if i >= len(got) || got[i] != want[i] {
				t.Fatalf("body rows:\n%s\nwant row %d\n%q", strings.Join(got, "\n"), i, want[i])
			}
		}
	}
	// check draws plain lines and compares the rows at the given indexes.
	check := func(t *testing.T, cols, rows int, name string, wrap bool, lines []string, want map[int]string) {
		t.Helper()
		got, _ := draw(t, cols, rows, name, wrap, lines, nil)
		rowsAre(t, got, want)
	}

	t.Run("wrap on, ASCII line of ten rows", func(t *testing.T) {
		// 1,040 bytes cut every 107: the last row starts at 963 and holds
		// the last 77.
		check(t, 200, 60, "notes.txt", true, []string{strings.Repeat(alpha, 40)}, map[int]string{
			0:  strings.Repeat(alpha, 4) + "abc",
			1:  "defghijklmnopqrstuvwxyz" + strings.Repeat(alpha, 3) + "abcdef",
			2:  "ghijklmnopqrstuvwxyz" + strings.Repeat(alpha, 3) + "abcdefghi",
			9:  "bcdefghijklmnopqrstuvwxyz" + strings.Repeat(alpha, 2),
			10: "",
		})
	})

	t.Run("wrap on, a wide character across the row edge", func(t *testing.T) {
		// 300 wide characters, 600 cells, 900 bytes. Character 53 takes
		// columns 106 and 107, across the edge of the first row at 107: that
		// row ends in the ellipsis and the next row starts with it, one
		// column late, so its end again falls on a character that does not
		// fit (106) and ends in the ellipsis. The third row starts at column
		// 214, character 107, and ends the same way before character 160,
		// which crosses the next edge at 321.
		check(t, 200, 60, "notes.txt", true, []string{strings.Repeat(han, 30)}, map[int]string{
			0: strings.Repeat(han, 5) + "〇一二…",
			1: "三四五六七八九" + strings.Repeat(han, 4) + "〇一二三四五…",
			2: "七八九" + strings.Repeat(han, 5) + "…",
		})
	})

	t.Run("wrap on, a highlighted run across the row edge", func(t *testing.T) {
		// 788 bytes: x, an operator, the number 1, then a string from column
		// 6 to the end, across the edges at 107 and 214.
		line := `x := 1"` + strings.Repeat(alpha, 30) + `"`
		lines := []string{line}
		spans := diffview.Highlight("main.go", lines)
		got, c := draw(t, 200, 60, "main.go", true, lines, spans)
		rowsAre(t, got, map[int]string{
			0: `x := 1"` + strings.Repeat(alpha, 3) + "abcdefghijklmnopqrstuv",
			1: "wxyz" + strings.Repeat(alpha, 3) + "abcdefghijklmnopqrstuvwxy",
			2: "z" + strings.Repeat(alpha, 4) + "ab",
		})
		fg := func(col, row int) color.Color { return c.buf.CellAt(1+col, 1+row).Style.Fg }
		same := func(a, b color.Color) bool {
			if a == nil || b == nil {
				return a == b
			}
			ar, ag, ab, aa := a.RGBA()
			br, bg, bb, ba := b.RGBA()
			return ar == br && ag == bg && ab == bb && aa == ba
		}
		plain, number, str := fg(0, 0), fg(5, 0), fg(106, 0)
		if same(plain, number) || same(str, plain) || same(str, number) {
			t.Fatalf("the theme draws a name (%v), a number (%v) and a string (%v) in colours that are not all apart", plain, number, str)
		}
		for row := 1; row <= 2; row++ {
			if got := fg(0, row); !same(got, str) {
				t.Errorf("the first cell of row %d, inside the string, is drawn in %v, want the string's %v", row, got, str)
			}
		}
	})

	t.Run("wrap off, a line longer than the row", func(t *testing.T) {
		// 1,040 bytes.
		check(t, 200, 60, "data.log", false, []string{strings.Repeat(alpha, 40)}, map[int]string{
			0: strings.Repeat(alpha, 4) + "ab…",
			1: "",
		})
	})

	t.Run("wrap off, the whole line's ellipsis at the row's width and one cell more", func(t *testing.T) {
		// Both lines are under the 324 bytes and drawn whole. 53 wide
		// characters and one ASCII cell fill the 107 cells (160 bytes); 54
		// wide characters are 108 cells (162 bytes) and end in the ellipsis.
		check(t, 200, 60, "data.log", false, []string{
			strings.Repeat(han, 5) + "〇一二x",
			strings.Repeat(han, 5) + "〇一二三",
		}, map[int]string{
			0: strings.Repeat(han, 5) + "〇一二x",
			1: strings.Repeat(han, 5) + "〇一二…",
			2: "",
		})
	})

	t.Run("wrap on, a 100 and a 200 byte line in a narrow panel", func(t *testing.T) {
		// Rows of 45 cells; a line is drawn whole up to 141 bytes, so the
		// first line is and the second is not.
		check(t, 60, 30, "notes.txt", true, []string{
			strings.Repeat(digits, 10),
			strings.Repeat(alpha, 7) + "abcdefghijklmnopqr",
		}, map[int]string{
			0: strings.Repeat(digits, 4) + "01234",
			1: "56789" + strings.Repeat(digits, 4),
			2: digits,
			3: alpha + "abcdefghijklmnopqrs",
			4: "tuvwxyz" + alpha + "abcdefghijkl",
			5: "mnopqrstuvwxyz" + alpha + "abcde",
			6: "fghijklmnopqrstuvwxyz" + "abcdefghijklmnopqrstuvwx",
			7: "yzabcdefghijklmnopqr",
			8: "",
		})
	})

	// The rows of the next two cases pin how the whole line draws today,
	// including an ellipsis it shows early or where no text was cut: it
	// decides that a line goes on from the width of its graphemes but cuts
	// it in smaller units, which a character and its modifier, or a keycap,
	// count wider or narrower. A later fix of that draw changes these rows on
	// purpose.
	t.Run("pairs counted wider than their graphemes", func(t *testing.T) {
		// Wrap off: 109 pairs of q and a skin tone modifier, one cell each
		// on screen and three where the line is cut, then 100 digits (645
		// bytes). The cut at 106 cells falls after 35 pairs and a q.
		skin := strings.Repeat("q\U0001F3FD", 109)
		check(t, 200, 60, "data.log", false, []string{skin + strings.Repeat(digits, 10)}, map[int]string{
			0: strings.Repeat("q\U0001F3FD", 35) + "q…",
			1: "",
		})
		// Wrap on: a row of letters, 60 pairs of q and the halfwidth
		// semi-voiced mark, one cell each on screen and two where the line is
		// cut, then 400 digits (747 bytes). The second row starts at the
		// pairs and is cut at 107 cells, after 53 pairs and a q.
		halfw := strings.Repeat("qﾟ", 60)
		check(t, 200, 60, "notes.txt", true, []string{strings.Repeat(alpha, 4) + "abc" + halfw + strings.Repeat(digits, 40)}, map[int]string{
			0: strings.Repeat(alpha, 4) + "abc",
			1: strings.Repeat("qﾟ", 53) + "q…",
		})
	})

	t.Run("keycaps before the last row of a wrapped line", func(t *testing.T) {
		// A row of letters, five keycaps (two cells each on screen, one
		// where the line is cut), then 314 digits: 456 bytes and 426 cells
		// as the line is cut, so the last row starts at 321 and holds 105
		// digits. The line reads five cells wider, so that row ends in the
		// ellipsis and an empty row follows.
		keycaps := strings.Repeat("1️⃣", 5)
		check(t, 200, 60, "notes.txt", true, []string{strings.Repeat(alpha, 4) + "abc" + keycaps + strings.Repeat(digits, 31) + "0123"}, map[int]string{
			0: strings.Repeat(alpha, 4) + "abc",
			2: "23456789" + strings.Repeat(digits, 9) + "012345678",
			3: "9" + strings.Repeat(digits, 10) + "0123…",
			4: "",
			5: "",
		})
	})
}
