package vt_test

import (
	"fmt"
	"image/color"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"tuios.dev/vt"
)

type conformCase struct {
	name string

	cols, rows int

	in    string
	split []string

	resize [][2]int
	then   string

	history *string

	want string

	cursor string

	region string

	cells []cellWant

	unhandled bool

	knownBug string

	skip string
}

type unhandledLog struct {
	lines []string
}

func (u *unhandledLog) Printf(format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	if strings.HasPrefix(msg, "unhandled sequence") {
		u.lines = append(u.lines, msg)
	}
}

type cellWant struct {
	x, y int

	content string
	width   int

	fg, bg, ul color.Color
	underline  *uv.Underline
	attrs      *uint8

	link       *string
	linkParams *string
}

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

func dumpHistory(emu *vt.Emulator) string {
	var b strings.Builder
	for i := range emu.ScrollbackLen() {
		var line strings.Builder
		for _, c := range emu.ScrollbackLine(i) {
			switch {
			case c.Content == "" && c.Width == 0:
			case c.Content == "":
				line.WriteByte(' ')
			default:
				line.WriteString(c.Content)
			}
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
	}
	return b.String()
}

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

func runConform(t *testing.T, cases []conformCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skip != "" {
				t.Skip(tc.skip)
			}
			emu, log := newConformEmulator(t, tc)
			problems := conformProblems(emu, log, tc)

			if tc.knownBug == "" {
				for _, p := range problems {
					t.Error(p)
				}
				return
			}
			if len(problems) == 0 {
				t.Errorf("this case is marked as a known bug (%s) but passed; "+
					"delete the knownBug field", tc.knownBug)
			}
		})
	}
}

func newConformEmulator(t *testing.T, tc conformCase) (*vt.Emulator, *unhandledLog) {
	t.Helper()
	cols, rows := tc.cols, tc.rows
	if cols == 0 {
		cols = 6
	}
	if rows == 0 {
		rows = 4
	}
	emu := vt.NewEmulator(cols, rows)
	log := &unhandledLog{}
	emu.SetLogger(log)
	writes := tc.split
	if writes == nil {
		writes = []string{tc.in}
	}
	for _, w := range writes {
		if _, err := emu.WriteString(w); err != nil {
			t.Fatalf("write %q: %v", w, err)
		}
	}
	for _, sz := range tc.resize {
		emu.Resize(sz[0], sz[1])
	}
	if tc.then != "" {
		if _, err := emu.WriteString(tc.then); err != nil {
			t.Fatalf("write %q: %v", tc.then, err)
		}
	}
	return emu, log
}

func conformProblems(emu *vt.Emulator, log *unhandledLog, tc conformCase) []string {
	var problems []string
	add := func(format string, v ...any) {
		problems = append(problems, fmt.Sprintf(format, v...))
	}

	if got, want := dumpScreen(emu), normalizeWant(tc.want); got != want {
		add("screen mismatch\n--- got ---\n%s\n--- want ---\n%s\n--- end ---", boxed(got), boxed(want))
	}

	if tc.history != nil {
		if got, want := dumpHistory(emu), *tc.history; got != want {
			add("history mismatch\n--- got ---\n%s\n--- want ---\n%s\n--- end ---", boxed(got), boxed(want))
		}
	}

	if tc.cursor != "" {
		p := emu.CursorPosition()
		if got := fmt.Sprintf("%d,%d", p.X, p.Y); got != tc.cursor {
			add("cursor = %s, want %s", got, tc.cursor)
		}
	}

	if tc.region != "" {
		r := emu.ScrollRegion()
		got := fmt.Sprintf("%d,%d-%d,%d", r.Min.X, r.Min.Y, r.Max.X, r.Max.Y)
		if got != tc.region {
			add("scroll region = %s, want %s", got, tc.region)
		}
	}

	switch {
	case tc.unhandled && len(log.lines) == 0:
		add("expected the emulator to reject this input, but it recognised all of it")
	case !tc.unhandled && len(log.lines) > 0:
		add("the emulator did not recognise part of this input, so whatever the "+
			"screen shows is what ignoring it looks like: %s", strings.Join(log.lines, "; "))
	}

	for _, cw := range tc.cells {
		problems = append(problems, cellProblems(emu, cw)...)
	}
	return problems
}

func cellProblems(emu *vt.Emulator, cw cellWant) []string {
	var problems []string
	add := func(format string, v ...any) {
		problems = append(problems, fmt.Sprintf(format, v...))
	}

	c := emu.CellAt(cw.x, cw.y)
	if c == nil {
		add("cell(%d,%d) is out of bounds", cw.x, cw.y)
		return problems
	}
	if cw.content != "" && c.Content != cw.content {
		add("cell(%d,%d).Content = %q, want %q", cw.x, cw.y, c.Content, cw.content)
	}
	if cw.width != 0 && c.Width != cw.width {
		add("cell(%d,%d).Width = %d, want %d", cw.x, cw.y, c.Width, cw.width)
	}
	if cw.fg != nil && !sameColor(c.Style.Fg, cw.fg) {
		add("cell(%d,%d).Fg = %v, want %v", cw.x, cw.y, c.Style.Fg, cw.fg)
	}
	if cw.bg != nil && !sameColor(c.Style.Bg, cw.bg) {
		add("cell(%d,%d).Bg = %v, want %v", cw.x, cw.y, c.Style.Bg, cw.bg)
	}
	if cw.ul != nil && !sameColor(c.Style.UnderlineColor, cw.ul) {
		add("cell(%d,%d).UnderlineColor = %v, want %v", cw.x, cw.y, c.Style.UnderlineColor, cw.ul)
	}
	if cw.underline != nil && c.Style.Underline != *cw.underline {
		add("cell(%d,%d).Underline = %v, want %v", cw.x, cw.y, c.Style.Underline, *cw.underline)
	}
	if cw.attrs != nil && c.Style.Attrs != *cw.attrs {
		add("cell(%d,%d).Attrs = %08b, want %08b", cw.x, cw.y, c.Style.Attrs, *cw.attrs)
	}
	if cw.link != nil && c.Link.URL != *cw.link {
		add("cell(%d,%d).Link.URL = %q, want %q", cw.x, cw.y, c.Link.URL, *cw.link)
	}
	if cw.linkParams != nil && c.Link.Params != *cw.linkParams {
		add("cell(%d,%d).Link.Params = %q, want %q", cw.x, cw.y, c.Link.Params, *cw.linkParams)
	}
	return problems
}

func sameColor(got, want color.Color) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	gr, gg, gb, ga := got.RGBA()
	wr, wg, wb, wa := want.RGBA()
	return gr == wr && gg == wg && gb == wb && ga == wa
}

func boxed(s string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(s, "\n") {
		b.WriteByte('|')
		b.WriteString(line)
		b.WriteString("|\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

var underlineNone = ansi.UnderlineNone

func indexed(i int) color.Color { return vt.NewEmulator(1, 1).IndexedColor(i) }
