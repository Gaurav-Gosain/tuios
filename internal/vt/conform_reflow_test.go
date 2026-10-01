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
// row behind. A prompt the shell marked open with OSC 133, its start (A) and
// the start of the command line (B) with no C after, is not reflowed, so the
// repaint lands where it should and no row is added. A lone A is not an open
// prompt: a shell that marks only its prompts leaves A over every command's
// output.
func TestReflowLeavesAMarkedPromptToTheShell(t *testing.T) {
	const w, h = 31, 10
	for _, tc := range []struct {
		name     string
		a, b     string
		wantRows int
	}{
		{"open prompt", "\x1b]133;A\x07", "\x1b]133;B\x07", 0},
		{"lone A", "\x1b]133;A\x07", "", 4},
		{"no marks", "", "", 4},
	} {
		emu := vt.NewEmulator(w, h)
		var b strings.Builder
		for range h + 5 {
			b.WriteString("row\r\n")
		}
		b.WriteString(tc.a + strings.Repeat("p", w) + "\r\n> " + tc.b)
		_, _ = emu.WriteString(b.String())
		start := emu.ScrollbackLen()
		for i := 1; i <= 4; i++ {
			nw := w - i
			emu.Resize(nw, h)
			// fish's repaint: up one row onto the prompt, redraw both rows.
			_, _ = emu.WriteString("\r\r\x1b[A\x1b[K" + strings.Repeat("p", nw) + "\r\n> \x1b[J\r\x1b[2C")
		}
		// The positive halves: without an open prompt the same repaint
		// costs a row each time, so the first case is testing the marks.
		if got := emu.ScrollbackLen() - start; got != tc.wantRows {
			t.Errorf("%s: the prompt left %d rows behind over 4 narrowings, want %d", tc.name, got, tc.wantRows)
		}
	}
}

// TestReflowDoesNotCutOutputUnderALoneA: a shell that marks only its prompt
// start, as foot's minimal PS1 does, leaves A standing over the output of
// every command it runs. That output reflows like any other text: narrowing
// and widening again gives every character back.
func TestReflowDoesNotCutOutputUnderALoneA(t *testing.T) {
	long := strings.Repeat("0123456789", 7)
	emu := vt.NewEmulator(40, 8)
	_, _ = emu.WriteString("\x1b]133;A\x07$ seq\r\n" + long + "\r\n")
	emu.Resize(20, 8)
	emu.Resize(40, 8)
	if got := emuText(emu); !strings.Contains(got, long) {
		t.Errorf("the 70-character line did not survive 40 to 20 to 40 columns:\n%s", got)
	}
}

// TestReflowKeepsAnOpenPromptRowWhole: an open prompt's row is kept on one
// row, and what does not fit a narrower screen is held off the edge rather
// than cut. When the shell does not repaint, as when a drag ends at the size
// it started at, widening again shows the whole command line.
func TestReflowKeepsAnOpenPromptRowWhole(t *testing.T) {
	cmd := "echo TYPED-abcdefghijklmnopqrst"
	for _, tc := range []struct {
		name  string
		sizes [][2]int
	}{
		{"narrow and back", [][2]int{{12, 6}, {40, 6}}},
		{"narrow, shorter, and back", [][2]int{{12, 6}, {9, 2}, {40, 6}}},
	} {
		emu := vt.NewEmulator(40, 6)
		_, _ = emu.WriteString("out\r\n\x1b]133;A\x07$ \x1b]133;B\x07" + cmd)
		for _, sz := range tc.sizes {
			emu.Resize(sz[0], sz[1])
		}
		if got := emuText(emu); !strings.Contains(got, "$ "+cmd) {
			t.Errorf("%s: the typed command did not come back whole:\n%s", tc.name, got)
		}
		// The shell's next write to the row drops what was held, as it
		// would after a repaint.
		emu.Resize(12, 6)
		_, _ = emu.WriteString("\r\x1b[K$ x")
		emu.Resize(40, 6)
		if got := emuText(emu); strings.Contains(got, "TYPED") {
			t.Errorf("%s: a repainted row kept the cells it held before:\n%s", tc.name, got)
		}
	}
}

// emuText is the history and the screen as text, one line the guest printed
// to a line of text.
func emuText(emu *vt.Emulator) string {
	var b strings.Builder
	for i := range emu.ScrollbackLen() {
		for _, c := range emu.ScrollbackLine(i) {
			b.WriteString(c.Content)
		}
		if w, _ := emu.ScrollbackSoftWrapped(i); !w {
			b.WriteByte('\n')
		}
	}
	for y := range emu.Height() {
		for x := range emu.Width() {
			b.WriteString(emu.CellAt(x, y).Content)
		}
		if w, _ := emu.RowSoftWrapped(y); !w {
			b.WriteByte('\n')
		}
	}
	return b.String()
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

func TestConform_ReflowSavedAndPending(t *testing.T) {
	runConform(t, []conformCase{
		{
			// A program that saves the cursor after a label, and comes back
			// to it after the pane was narrowed and widened. The label
			// filled its last row at the narrow width, and the saved place
			// went to that row's first column, over the label.
			name: "a saved cursor just past text that fills a row stays after it",
			cols: 40, rows: 4,
			in:     "Progress: \x1b7\r\n",
			resize: [][2]int{{5, 4}, {40, 4}},
			then:   "\x1b8done",
			want:   "Progress: done",
		},
		{
			// A resize that does not reflow, here a taller screen with the
			// cursor above the bottom, kept the column but dropped the
			// pending wrap, so the next character overwrote the last one.
			name: "a pending wrap survives a resize that does not reflow",
			cols: 5, rows: 3,
			in:     "abcde",
			resize: [][2]int{{5, 4}},
			then:   "X",
			want:   "abcde\nX",
			cursor: "1,1",
		},
	})
}

// TestReflowOpenPromptRepaintDropsTheTail: the shell repaints its prompt row
// after a narrowing, with plain characters and no erase. The cells the row
// held past the width are from before the repaint, and must not come back
// when the pane widens.
func TestReflowOpenPromptRepaintDropsTheTail(t *testing.T) {
	emu := vt.NewEmulator(40, 4)
	_, _ = emu.WriteString("\x1b]133;A\x07~/src $ echo " + strings.Repeat("A", 25) + "\r\n> \x1b]133;B\x07")
	emu.Resize(20, 4)
	_, _ = emu.WriteString("\x1b[A\r~/src $ echo BBBBBBB")
	emu.Resize(40, 4)
	got := emuText(emu)
	if !strings.Contains(got, "~/src $ echo BBBBBBB") {
		t.Fatalf("the repainted row is not on the screen, so this tests nothing:\n%s", got)
	}
	if strings.Contains(got, "BA") || strings.Contains(got, "AAAA") {
		t.Errorf("the repainted row came back with cells from before the repaint:\n%s", got)
	}
}

// TestReflowCursorOnAnOpenPromptComesBack: the cursor at the end of a long
// command typed on an open prompt is past a narrower width. It stands on the
// last column while narrow, and comes back to its own column when the pane
// widens, so the next character typed goes after the command.
func TestReflowCursorOnAnOpenPromptComesBack(t *testing.T) {
	emu := vt.NewEmulator(60, 4)
	// 2 + 5 + 87 = 94 columns: the second row holds 34, and the cursor
	// stands at column 34.
	_, _ = emu.WriteString("\x1b]133;A\x07$ \x1b]133;B\x07echo " + strings.Repeat("x", 87))
	if p := emu.CursorPosition(); p.X != 34 {
		t.Fatalf("the cursor is at column %d before the resize, want 34", p.X)
	}
	emu.Resize(30, 4)
	emu.Resize(60, 4)
	_, _ = emu.WriteString("Z")
	if p := emu.CursorPosition(); p.X != 35 {
		t.Errorf("after narrowing and widening the cursor typed Z and stands at column %d, want 35", p.X)
	}
	if c := emu.CellAt(34, emu.CursorPosition().Y); c == nil || c.Content != "Z" {
		t.Errorf("Z is not at column 34:\n%s", emuText(emu))
	}
}
