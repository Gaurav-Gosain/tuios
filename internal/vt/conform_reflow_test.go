package vt_test

// Conformance for reflow: what a width change does to the main screen.
//
// The main screen lays out again the lines the guest printed, joining the rows
// autowrap split and splitting them again at the new width, as ghostty, kitty
// and tmux do. A narrowing resize used to cut every column past the new edge
// off the screen for good. The alternate screen does not reflow: the program
// on it redraws.

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

func TestConform_Reflow(t *testing.T) {
	runConform(t, []conformCase{
		{
			name: "a narrower screen wraps a line instead of cutting it",
			cols: 10, rows: 4,
			in:      "0123456789\r\n$ ",
			resize:  [][2]int{{4, 4}},
			want:    "0123\n4567\n89\n$",
			history: ptr(""),
			cursor:  "2,3",
		},
		{
			name: "widening again joins the rows a narrowing split",
			cols: 10, rows: 4,
			in:      "0123456789\r\n$ ",
			resize:  [][2]int{{4, 4}, {10, 4}},
			want:    "0123456789\n$",
			history: ptr(""),
			cursor:  "2,1",
		},
		{
			name: "a soft-wrapped line joins when the screen widens",
			cols: 4, rows: 4,
			in:     "abcdefghij\r\n$ ",
			resize: [][2]int{{12, 4}},
			want:   "abcdefghij\n$",
			cursor: "2,1",
		},
		{
			// The rows a reflow adds go into the history from the top, and
			// the cursor stays on the last row with the prompt.
			name: "rows a narrowing adds push the oldest into the history",
			cols: 8, rows: 3,
			in:      "aaaaaaaa\r\nbbbbbbbb\r\n$ ",
			resize:  [][2]int{{4, 3}},
			want:    "bbbb\nbbbb\n$",
			history: ptr("aaaa\naaaa"),
			cursor:  "2,2",
		},
		{
			name: "a wide character that no longer fits moves whole to the next row",
			cols: 6, rows: 3,
			in:     "abcd世x\r\n",
			resize: [][2]int{{5, 3}},
			want:   "abcd\n世x",
			cursor: "0,2",
		},
		{
			name: "the padding a wide character left is not text when the line joins",
			cols: 5, rows: 3,
			in:     "abcd世x\r\n",
			resize: [][2]int{{7, 3}},
			want:   "abcd世x",
			cursor: "0,1",
		},
		{
			name: "a ZWJ emoji at the edge moves whole",
			cols: 6, rows: 3,
			in:     "abcd\U0001F468\u200d\U0001F469\u200d\U0001F467e\u0301\r\n",
			resize: [][2]int{{5, 3}, {6, 3}},
			want:   "abcd\U0001F468\u200d\U0001F469\u200d\U0001F467\ne\u0301",
			cursor: "0,2",
		},
		{
			// The cursor stands past the prompt, and the line keeps the
			// column it stands on.
			name: "the cursor stays on its character in a long line",
			cols: 10, rows: 3,
			in:     "abcdefghijklmno\x1b[2;3H",
			resize: [][2]int{{5, 3}},
			want:   "abcde\nfghij\nklmno",
			cursor: "2,2",
		},
		{
			// A line that ends exactly at the edge leaves the cursor in
			// pending wrap; the next character after a reflow goes after
			// the last one, not over it.
			name: "a pending wrap carries across a widening",
			cols: 5, rows: 3,
			in:     "abcde",
			resize: [][2]int{{8, 3}},
			then:   "X",
			want:   "abcdeX",
			cursor: "6,0",
		},
		{
			name: "a pending wrap stays pending when the line still ends at the edge",
			cols: 4, rows: 3,
			in:     "abcdefgh",
			resize: [][2]int{{8, 3}},
			then:   "X",
			want:   "abcdefgh\nX",
			cursor: "1,1",
		},
		{
			name: "a taller screen takes lines back from the history",
			cols: 10, rows: 2,
			in:      "l1\r\nl2\r\nl3\r\n$ ",
			resize:  [][2]int{{10, 4}},
			want:    "l1\nl2\nl3\n$",
			history: ptr(""),
			cursor:  "2,3",
		},
		{
			// With the cursor above the bottom, a full-screen program owns
			// the rows, so nothing comes back from the history.
			name: "a taller screen with the cursor above the bottom adds blank rows",
			cols: 10, rows: 2,
			in:      "l1\r\nl2\r\nl3\x1b[1;1H",
			resize:  [][2]int{{10, 4}},
			want:    "l2\nl3",
			history: ptr("l1"),
			cursor:  "0,0",
		},
		{
			name: "the alternate screen does not reflow",
			cols: 10, rows: 3,
			in:     "\x1b[?1049h\x1b[H0123456789",
			resize: [][2]int{{5, 3}},
			want:   "01234",
		},
		{
			name: "a saved cursor moves with its character",
			cols: 10, rows: 3,
			in:     "abcdefghij\x1b[1;8H\x1b7\x1b[2;1H",
			resize: [][2]int{{5, 3}},
			then:   "\x1b8X",
			want:   "abcde\nfgXij",
		},
		{
			name: "colour and links stay on their characters across a wrap",
			cols: 8, rows: 3,
			in:     "ab\x1b[31m\x1b]8;;http://x\x07cdefgh\x1b]8;;\x07\x1b[m\r\n",
			resize: [][2]int{{4, 3}},
			want:   "abcd\nefgh",
			cells: []cellWant{
				{x: 0, y: 1, content: "e", fg: indexed(1), link: ptr("http://x")},
				{x: 1, y: 0, content: "b", link: ptr("")},
			},
		},
	})
}

// TestReflowMovesSemanticMarks: an OSC 133 mark names a row by its absolute
// index, and the scrollback browser reads the command and its output from
// there. A reflow moves rows, so the marks have to move with them, or the
// browser reads the wrong rows. The libghostty backend does not move them:
// its marks point at the rows the text was on before the reflow.
func TestReflowMovesSemanticMarks(t *testing.T) {
	emu := vt.NewEmulator(10, 4)
	for i := range 4 {
		_, _ = emu.WriteString("\x1b]133;A\x07p" + string(rune('0'+i)) + "-abcdefgh\r\n")
	}
	text := func(abs, col int) string {
		n := emu.ScrollbackLen()
		var line uv.Line
		if abs < n {
			line = emu.ScrollbackLine(abs)
		} else {
			for x := range emu.Width() {
				line = append(line, *emu.CellAt(x, abs-n))
			}
		}
		var b strings.Builder
		for _, c := range line[col:] {
			b.WriteString(c.Content)
		}
		return strings.TrimSpace(b.String())
	}
	for _, w := range []int{5, 3, 12, 10} {
		emu.Resize(w, 4)
		for i, m := range emu.SemanticMarkers().Markers() {
			want := "p" + string(rune('0'+i))
			if got := text(m.AbsLine, m.Col); !strings.HasPrefix(got, want) {
				t.Errorf("at width %d mark %d points at %q, want the row that starts %q", w, i, got, want)
			}
		}
	}
}

// TestReflowLeavesAMarkedPromptToTheShell: fish's prompt fills the pane's
// width exactly, and on SIGWINCH fish repaints it by stepping up the rows it
// took at the old width. Reflow makes a full-width line one row taller at a
// narrower width, so without help each narrowing leaves the prompt's first
// row behind. A prompt the shell marked with OSC 133, and that is still open,
// is not reflowed, so the repaint lands where it should and no row is added.
func TestReflowLeavesAMarkedPromptToTheShell(t *testing.T) {
	const w, h = 31, 10
	for _, marked := range []bool{true, false} {
		emu := vt.NewEmulator(w, h)
		var b strings.Builder
		for range h + 5 {
			b.WriteString("row\r\n")
		}
		if marked {
			b.WriteString("\x1b]133;A\x07")
		}
		b.WriteString(strings.Repeat("p", w) + "\r\n> ")
		_, _ = emu.WriteString(b.String())
		start := emu.ScrollbackLen()
		for i := 1; i <= 4; i++ {
			nw := w - i
			emu.Resize(nw, h)
			// fish's repaint: up one row onto the prompt, redraw both rows.
			_, _ = emu.WriteString("\r\r\x1b[A\x1b[K" + strings.Repeat("p", nw) + "\r\n> \x1b[J\r\x1b[2C")
		}
		got := emu.ScrollbackLen() - start
		switch {
		case marked && got != 0:
			t.Errorf("a marked prompt left %d rows behind over 4 narrowings, want 0", got)
		case !marked && got != 4:
			// The positive half: the same repaint without the marks does
			// cost a row each time, so the case above is testing the marks.
			t.Errorf("an unmarked prompt left %d rows behind over 4 narrowings, want 4", got)
		}
	}
}

// TestReflowReportsWhereRowsWent: the client places kitty images and
// text-sizing runs at a row counted from the oldest history row, and moves
// them with the remap a reflow reports. The remap has to send each row to the
// row its text is on now, in history or on the screen, across a ring that
// evicts lines as the reflow pushes rows back.
func TestReflowReportsWhereRowsWent(t *testing.T) {
	for _, ringCap := range []int{1000, 3} {
		emu := vt.NewEmulator(10, 4)
		emu.SetScrollbackMaxLines(ringCap)
		for i := range 6 {
			_, _ = emu.WriteString("r" + string(rune('0'+i)) + "-abcdefg\r\n")
		}
		_, _ = emu.WriteString("$ ")
		rowText := func(abs int) string {
			n := emu.ScrollbackLen()
			var b strings.Builder
			if abs < n {
				for _, c := range emu.ScrollbackLine(abs) {
					b.WriteString(c.Content)
				}
			} else if abs-n < emu.Height() {
				for x := range emu.Width() {
					b.WriteString(emu.CellAt(x, abs-n).Content)
				}
			}
			return strings.TrimSpace(b.String())
		}
		// Every row on the screen, by its text.
		rows := map[int]string{}
		for y := range emu.Height() {
			abs := emu.ScrollbackLen() + y
			if txt := rowText(abs); txt != "" {
				rows[abs] = txt
			}
		}
		var remap func(int) int
		emu.SetReflowFunc(func(r func(int) int) { remap = r })
		emu.Resize(5, 4)
		if remap == nil {
			t.Fatalf("ring %d: the reflow reported nothing", ringCap)
		}
		for abs, txt := range rows {
			now := remap(abs)
			if now < 0 {
				continue // evicted with its line
			}
			if got := rowText(now); !strings.HasPrefix(txt, got) || got == "" {
				t.Errorf("ring %d: row %d %q went to row %d, which holds %q", ringCap, abs, txt, now, got)
			}
		}
	}
}
