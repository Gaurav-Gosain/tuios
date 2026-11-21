package app

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

var clipSeeds = []string{
	"",
	"\n",
	"\n\n\n",
	"hello",
	"hello\nworld",
	"\nsecond line has content\nthird",
	"\n\n\ncontent far down",
	"\x1b[0m",
	"\x1b[31m\x1b[0m\n\x1b[32mgreen\x1b[0m",
	"\x1b[38;2;1;2;3m\x1b[48;5;9m\x1b[1m\x1b[4m",
	"\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\",
	"\x1b]0;title\x07text",
	"\x1b",
	"\x1b[",
	"\x1b[31",
	"\x1b]8;;",
	"\x1b]",
	"a\x1bb\x1b[c",
	strings.Repeat("\xe4\xb8\x96", 40),
	"ab\xe4\xb8\x96cd",
	strings.Repeat("\xf0\x9f\x91\x8d", 40),
	"\x1b[31m\xe4\xb8\x96\x1b[0m\xe4\xb8\x96",
	"e\xcc\x81\xcc\x82\xcc\x83",
	"\xf0\x9f\x91\xa8\xe2\x80\x8d\xf0\x9f\x91\xa9",
	strings.Repeat("x", 500),
	strings.Repeat("line\n", 200),
	strings.Repeat(strings.Repeat("y", 300)+"\n", 100),
	"a\nbb\nccc\n" + strings.Repeat("d", 400),
	strings.Repeat("z", 400) + "\na\nb",
	"a\tb\tc",
	"\x00\x01\x07\x08",
	"a\rb",
	"ab\xffcd",
	"\xff\xfe\xfd",
}

// FuzzClipWindowContent asserts the contract the compositor relies on: the clip
// never returns more rows or columns than the viewport offered, never reports a
// placement outside the viewport, and never discards content that has any
// visible overlap with the viewport.
//
// That last one is the invariant a user-visible bug broke: a pane whose frame
// began with an empty line measured as zero wide, so the offscreen guard threw
// the entire frame away and the pane composited as bare background.
func FuzzClipWindowContent(f *testing.F) {
	for _, s := range clipSeeds {
		f.Add(s, 0, 0, 80, 24)
		f.Add(s, -5, -3, 80, 24)
		f.Add(s, 70, 20, 80, 24)
	}
	f.Add("", 0, 0, 0, 0)
	f.Add("x", -1000000, -1000000, 80, 24)
	f.Add("x", 1000000, 1000000, 80, 24)
	f.Add("wide\ncontent", 0, 0, 1, 1)
	pane743 := "╭──────────╮\n│😀😀ab      │\n│你好世界xy  │\n│plain-line│\n╰──────────╯"
	for _, x := range []int{-1, -2, -3, -4, -5, -6, -9} {
		f.Add(pane743, x, 0, 80, 24)
	}
	f.Add(pane743, 74, 0, 80, 24)
	f.Add(pane743, 75, 0, 80, 24)

	f.Fuzz(func(t *testing.T, content string, x, y, vw, vh int) {
		vw = max(vw%512+1, 1)
		vh = max(vh%512+1, 1)
		x = clampFuzzCoord(x)
		y = clampFuzzCoord(y)
		if len(content) > 1<<16 {
			content = content[:1<<16]
		}

		got, finalX, finalY := clipWindowContent(content, x, y, vw, vh)

		if finalX < 0 || finalY < 0 {
			t.Fatalf("clip placed content at negative (%d,%d)", finalX, finalY)
		}
		if got != "" && (finalX >= vw || finalY >= vh) {
			t.Fatalf("clip placed content at (%d,%d), outside a %dx%d viewport",
				finalX, finalY, vw, vh)
		}

		if got == "" {
			for i, line := range strings.Split(content, "\n") {
				row := y + i
				if row < 0 || row >= vh {
					continue
				}
				w := ansi.StringWidth(line)
				if w > 0 && x+w > 0 && x < vw {
					t.Fatalf("clip discarded a visible row: row %d (%d cells) "+
						"at (%d,%d) in a %dx%d viewport", i, w, x, y, vw, vh)
				}
			}
			return
		}

		gotLines := strings.Split(got, "\n")
		if maxRows := vh - finalY; len(gotLines) > maxRows {
			t.Fatalf("clip returned %d rows for %d rows of space", len(gotLines), maxRows)
		}
		maxCols := vw - finalX
		for i, line := range gotLines {
			if w := ansi.StringWidth(line); w > maxCols {
				t.Fatalf("clip row %d is %d cells wide, viewport offered %d",
					i, w, maxCols)
			}
		}

		if utf8.ValidString(content) {
			srcLines := strings.Split(content, "\n")
			for i, line := range gotLines {
				src := srcLines[finalY-y+i]
				want := min(max(ansi.StringWidth(src)-(finalX-x), 0), maxCols)
				if w := ansi.StringWidth(line); w != want {
					t.Fatalf("clip row %d is %d cells wide, want %d: %q from %q",
						i, w, want, line, src)
				}
			}
		}

		if srcRows := strings.Count(content, "\n") + 1; len(gotLines) > srcRows {
			t.Fatalf("clip returned %d rows from %d rows of content",
				len(gotLines), srcRows)
		}

		again, againX, againY := clipWindowContent(got, finalX, finalY, vw, vh)
		if againX != finalX || againY != finalY {
			t.Fatalf("re-clipping moved the content from (%d,%d) to (%d,%d)",
				finalX, finalY, againX, againY)
		}
		if w1, w2 := maxLineWidth(again), maxLineWidth(got); w1 > w2 {
			t.Fatalf("re-clipping widened the content from %d to %d cells", w2, w1)
		}
	})
}

func maxLineWidth(s string) int {
	w := 0
	for line := range strings.SplitSeq(s, "\n") {
		if lw := ansi.StringWidth(line); lw > w {
			w = lw
		}
	}
	return w
}

func clampFuzzCoord(v int) int {
	const bound = 1 << 20
	v %= bound
	return v
}

// FuzzClipWindowContentNoPanicOnEscapes drives the left cut (skipCells), which
// is only reached when the content has to be clipped from the left. It walks
// the bytes of every sequence before the cut and is the part most likely to
// run off the end of a truncated sequence.
func FuzzClipWindowContentNoPanicOnEscapes(f *testing.F) {
	for _, s := range clipSeeds {
		f.Add(s, 10)
	}
	f.Add("\x1b[31m"+strings.Repeat("a", 100), 50)
	f.Add(strings.Repeat("\x1b", 100), 50)
	f.Add(strings.Repeat("\x1b]", 100), 50)
	f.Add(strings.Repeat("\x1b[", 100), 50)
	f.Add("\x1b]8;;http://x\x1b\\"+strings.Repeat("b", 100), 50)

	f.Fuzz(func(t *testing.T, content string, shift int) {
		if len(content) > 1<<16 {
			content = content[:1<<16]
		}
		shift = max(shift%1024+1, 1)

		const vw, vh = 80, 24
		got, finalX, finalY := clipWindowContent(content, -shift, 0, vw, vh)

		if finalX != 0 {
			t.Fatalf("left-clipped content placed at x=%d, want 0", finalX)
		}
		if finalY != 0 {
			t.Fatalf("content placed at y=%d, want 0", finalY)
		}
		for i, line := range strings.Split(got, "\n") {
			if got == "" {
				break
			}
			if w := ansi.StringWidth(line); w > vw {
				t.Fatalf("left-clipped row %d is %d cells wide, viewport is %d",
					i, w, vw)
			}
		}
	})
}
