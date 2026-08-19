package vt_test

// A table-driven conformance corpus for the escape sequences a guest can send.
//
// The shape is borrowed from ghostty's terminal tests: run a byte string into a
// small screen, then compare a plain-text dump of that screen against a literal
// written the way it should look. A small screen is the point. On 80x24 an
// off-by-one at a margin hides inside 79 blanks, and the failure prints as two
// walls of whitespace; on 6x4 the diff is the bug.
//
// Cases assert the screen, and optionally the cursor, the scroll region, the
// pending-wrap state and per-cell style. Anything a case does not name is not
// asserted, so a case stays readable and stays about one thing.

import (
	"fmt"
	"image/color"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// conformCase is one input-to-screen-state pair.
type conformCase struct {
	// name identifies the case in test output.
	name string

	// cols and rows size the screen. Zero means 6x4, which is large enough for
	// margins and wrapping and small enough to read in a diff.
	cols, rows int

	// in is the byte stream fed to the emulator. Cases split it across writes
	// when the split itself is under test.
	in    string
	split []string

	// want is the expected screen, one line per row, trailing blanks trimmed.
	// A row of a wide grapheme prints the cluster once, in its lead column.
	want string

	// cursor, when set, is the expected cursor as "col,row", zero-based.
	cursor string

	// region, when set, is the expected scroll region as "left,top-right,bottom"
	// with the maxima exclusive, matching uv.Rectangle.
	region string

	// cells asserts style or content on individual cells.
	cells []cellWant

	// skip, when set, is the reason this case does not run.
	skip string
}

// cellWant asserts one cell. Only the fields set are compared, so a case that
// cares about a foreground colour does not have to spell out a whole style.
type cellWant struct {
	x, y int

	content string // "" means do not compare
	width   int    // 0 means do not compare

	fg, bg, ul color.Color // nil means do not compare
	underline  *uv.Underline
	attrs      *uint8

	link *string // hyperlink URL; nil means do not compare
}

// dumpScreen renders the visible screen as plain text: one line per row, every
// row present, trailing blanks trimmed. A wide cluster prints once, in its lead
// column, so a dump has one character per occupied cell and the reader can
// count columns.
func dumpScreen(emu *vt.Emulator) string {
	var b strings.Builder
	w, h := emu.Width(), emu.Height()
	for y := range h {
		var line strings.Builder
		for x := 0; x < w; {
			c := emu.CellAt(x, y)
			if c == nil {
				line.WriteByte(' ')
				x++
				continue
			}
			switch {
			case c.Content == "":
				// A continuation cell of the wide cluster to its left. It is
				// already accounted for by the lead, so it contributes nothing.
				line.WriteByte(' ')
			default:
				line.WriteString(c.Content)
			}
			if c.Width > 1 {
				x += c.Width
			} else {
				x++
			}
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		if y != h-1 {
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// normalizeWant tidies a case's expected screen: trailing blank rows and
// trailing blanks within a row are dropped, matching what dumpScreen produces.
// Leading blank rows are kept, because a case that expects the top of the
// screen to be empty is making a claim about it.
func normalizeWant(want string) string {
	want = strings.TrimRight(want, "\n")
	lines := strings.Split(want, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// runConform runs a table of cases.
func runConform(t *testing.T, cases []conformCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skip != "" {
				t.Skip(tc.skip)
			}
			emu := newConformEmulator(t, tc)
			checkConform(t, emu, tc)
		})
	}
}

func newConformEmulator(t *testing.T, tc conformCase) *vt.Emulator {
	t.Helper()
	cols, rows := tc.cols, tc.rows
	if cols == 0 {
		cols = 6
	}
	if rows == 0 {
		rows = 4
	}
	emu := vt.NewEmulator(cols, rows)
	writes := tc.split
	if writes == nil {
		writes = []string{tc.in}
	}
	for _, w := range writes {
		if _, err := emu.WriteString(w); err != nil {
			t.Fatalf("write %q: %v", w, err)
		}
	}
	return emu
}

func checkConform(t *testing.T, emu *vt.Emulator, tc conformCase) {
	t.Helper()

	if got, want := dumpScreen(emu), normalizeWant(tc.want); got != want {
		t.Errorf("screen mismatch\n--- got ---\n%s\n--- want ---\n%s\n--- end ---", boxed(got), boxed(want))
	}

	if tc.cursor != "" {
		p := emu.CursorPosition()
		if got := fmt.Sprintf("%d,%d", p.X, p.Y); got != tc.cursor {
			t.Errorf("cursor = %s, want %s", got, tc.cursor)
		}
	}

	if tc.region != "" {
		r := emu.ScrollRegion()
		got := fmt.Sprintf("%d,%d-%d,%d", r.Min.X, r.Min.Y, r.Max.X, r.Max.Y)
		if got != tc.region {
			t.Errorf("scroll region = %s, want %s", got, tc.region)
		}
	}

	for _, cw := range tc.cells {
		checkCell(t, emu, cw)
	}
}

func checkCell(t *testing.T, emu *vt.Emulator, cw cellWant) {
	t.Helper()
	c := emu.CellAt(cw.x, cw.y)
	if c == nil {
		t.Errorf("cell(%d,%d) is out of bounds", cw.x, cw.y)
		return
	}
	if cw.content != "" && c.Content != cw.content {
		t.Errorf("cell(%d,%d).Content = %q, want %q", cw.x, cw.y, c.Content, cw.content)
	}
	if cw.width != 0 && c.Width != cw.width {
		t.Errorf("cell(%d,%d).Width = %d, want %d", cw.x, cw.y, c.Width, cw.width)
	}
	if cw.fg != nil && !sameColor(c.Style.Fg, cw.fg) {
		t.Errorf("cell(%d,%d).Fg = %v, want %v", cw.x, cw.y, c.Style.Fg, cw.fg)
	}
	if cw.bg != nil && !sameColor(c.Style.Bg, cw.bg) {
		t.Errorf("cell(%d,%d).Bg = %v, want %v", cw.x, cw.y, c.Style.Bg, cw.bg)
	}
	if cw.ul != nil && !sameColor(c.Style.UnderlineColor, cw.ul) {
		t.Errorf("cell(%d,%d).UnderlineColor = %v, want %v", cw.x, cw.y, c.Style.UnderlineColor, cw.ul)
	}
	if cw.underline != nil && c.Style.Underline != *cw.underline {
		t.Errorf("cell(%d,%d).Underline = %v, want %v", cw.x, cw.y, c.Style.Underline, *cw.underline)
	}
	if cw.attrs != nil && c.Style.Attrs != *cw.attrs {
		t.Errorf("cell(%d,%d).Attrs = %#b, want %#b", cw.x, cw.y, c.Style.Attrs, *cw.attrs)
	}
	if cw.link != nil && c.Link.URL != *cw.link {
		t.Errorf("cell(%d,%d).Link.URL = %q, want %q", cw.x, cw.y, c.Link.URL, *cw.link)
	}
}

// sameColor compares by RGBA so an indexed colour and the same colour written
// as a struct still compare equal. A nil on either side only matches a nil.
func sameColor(got, want color.Color) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	gr, gg, gb, ga := got.RGBA()
	wr, wg, wb, wa := want.RGBA()
	return gr == wr && gg == wg && gb == wb && ga == wa
}

// boxed frames a screen dump so trailing blanks and blank rows are visible in a
// failure. Without it a row of spaces and an empty row print identically.
func boxed(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		b.WriteByte('|')
		b.WriteString(line)
		b.WriteString("|\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// ptr is a shorthand for the optional fields of cellWant.
func ptr[T any](v T) *T { return &v }
