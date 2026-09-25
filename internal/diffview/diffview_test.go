package diffview

import (
	"image/color"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

func TestPairs(t *testing.T) {
	C, A, D := Context, Add, Delete
	tests := []struct {
		name  string
		kinds []Kind
		want  []Pair
	}{
		{"context", []Kind{C, C}, []Pair{{0, 0}, {1, 1}}},
		{"replace one", []Kind{C, D, A, C}, []Pair{{0, 0}, {1, 2}, {3, 3}}},
		{"more removed", []Kind{D, D, A}, []Pair{{0, 2}, {1, -1}}},
		{"more added", []Kind{D, A, A}, []Pair{{0, 1}, {-1, 2}}},
		{"added alone", []Kind{C, A}, []Pair{{0, 0}, {-1, 1}}},
		{"removed alone", []Kind{D, C}, []Pair{{0, -1}, {1, 1}}},
		{"two blocks", []Kind{D, A, C, D, A}, []Pair{{0, 1}, {2, 2}, {3, 4}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Pairs(tc.kinds)
			if len(got) != len(tc.want) {
				t.Fatalf("Pairs = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("Pairs = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestChanged(t *testing.T) {
	tests := []struct {
		name, old, new   string
		wantOld, wantNew string
		wantNone         bool
	}{
		{name: "renamed word", old: "x := count + 1", new: "x := total + 1", wantOld: "count", wantNew: "total"},
		{name: "inserted", old: "f(a)", new: "f(a, b)", wantOld: "", wantNew: ", b"},
		{name: "inside a word widens to it", old: "value1 = 2", new: "value2 = 2", wantOld: "value1", wantNew: "value2"},
		{name: "rewritten", old: "time.Sleep(delay)", new: "log.Printf(\"x\")", wantNone: true},
		{name: "rewritten under shared indent", old: "        time.Sleep(delay)", new: "        return nil", wantNone: true},
		{name: "multibyte", old: "s := \"héllo\"", new: "s := \"hallo\"", wantOld: "héllo", wantNew: "hallo"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o, n := Changed(tc.old, tc.new)
			if tc.wantNone {
				if !o.Empty() || !n.Empty() {
					t.Fatalf("Changed marked %q and %q on lines with little in common", tc.old[o.Start:o.End], tc.new[n.Start:n.End])
				}
				return
			}
			if got := tc.old[o.Start:o.End]; got != tc.wantOld {
				t.Errorf("old part %q, want %q", got, tc.wantOld)
			}
			if got := tc.new[n.Start:n.End]; got != tc.wantNew {
				t.Errorf("new part %q, want %q", got, tc.wantNew)
			}
		})
	}
}

// classesOf is the class of the first byte of each word in a highlighted
// line.
func classAt(spans []Span, i int) Class {
	for _, s := range spans {
		if i >= s.Start && i < s.End {
			return s.Class
		}
	}
	return Plain
}

func TestHighlightGo(t *testing.T) {
	if !Enabled {
		t.Skip("this build does not highlight")
	}
	lines := []string{
		"func main() {",
		"    /* a comment",
		"       that goes on */",
		"    s := \"hi\" // done",
		"    n := 42",
		"}",
	}
	spans := Highlight("cmd/main.go", lines)
	if spans == nil {
		t.Fatal("a Go file was not highlighted")
	}
	checks := []struct {
		line int
		sub  string
		want Class
	}{
		{0, "func", Keyword},
		{0, "main", Func},
		{1, "/*", Comment},
		{2, "that", Comment}, // the second line of a block comment
		{3, "\"hi\"", String},
		{3, "// done", Comment},
		{4, "42", Number},
	}
	for _, c := range checks {
		i := strings.Index(lines[c.line], c.sub)
		if got := classAt(spans[c.line], i); got != c.want {
			t.Errorf("line %d %q is class %d, want %d (spans %v)", c.line, c.sub, got, c.want, spans[c.line])
		}
	}
	for i, s := range spans {
		for _, sp := range s {
			if sp.End > len(lines[i]) || sp.Start >= sp.End {
				t.Errorf("line %d has a span %v outside its %d bytes", i, sp, len(lines[i]))
			}
		}
	}
}

func TestHighlightLeavesUnknownAndHugeTextPlain(t *testing.T) {
	if Highlight("notes.unknownext", []string{"func x"}) != nil {
		t.Error("a file of no known type was highlighted")
	}
	if Highlight("big.go", []string{strings.Repeat("x", maxHighlightLine+1)}) != nil {
		t.Error("a line past the limit was tokenised")
	}
	if Enabled && Highlight("run", []string{"#!/bin/sh", "echo hi"}) == nil {
		t.Error("a script with a #! line was not highlighted")
	}
}

// cells draws s into a buffer width cells wide and returns its first row.
func cells(s string, width int) []*uv.Cell {
	buf := uv.NewScreenBuffer(width, 1)
	uv.NewStyledString(s).Draw(buf, buf.Bounds())
	out := make([]*uv.Cell, width)
	for x := range width {
		out[x] = buf.CellAt(x, 0)
	}
	return out
}

func testTheme() *Theme {
	return NewTheme(Palette{
		Ground: color.RGBA{0x1e, 0x1e, 0x2e, 0xff},
		Fg:     color.RGBA{0xcd, 0xd6, 0xf4, 0xff},
		Accent: color.RGBA{0x89, 0xb4, 0xfa, 0xff},
		Add:    color.RGBA{0xa6, 0xe3, 0xa1, 0xff},
		Delete: color.RGBA{0xf3, 0x8b, 0xa8, 0xff},
		Syntax: DefaultSyntax(),
	})
}

func TestCodeFillsItsWidthOnItsGround(t *testing.T) {
	th := testTheme()
	text := "func Do() error { return nil }"
	spans := []Span{{0, 4, Keyword}, {5, 7, Func}}
	for _, width := range []int{1, 2, 10, len(text), 60} {
		for _, xOff := range []int{0, 3, 100} {
			for _, kind := range []Kind{Context, Add, Delete, Missing} {
				out := th.Code(text, spans, Range{5, 7}, kind, xOff == 3, xOff, width)
				if w := ansi.StringWidth(out); w != width {
					t.Fatalf("width %d xOff %d kind %d: drew %d cells", width, xOff, kind, w)
				}
				for x, c := range cells(out, width) {
					if c.Style.Bg == nil {
						t.Fatalf("width %d xOff %d kind %d: cell %d has no background", width, xOff, kind, x)
					}
				}
			}
		}
	}
	for _, s := range []string{th.Gutter(12, 4, Add, false), th.Sign(Delete, true, true), th.Blank(Missing, false, 5), th.BlankGutter(Context, true, 3)} {
		for x, c := range cells(s, ansi.StringWidth(s)) {
			if c.Style.Bg == nil {
				t.Fatalf("%q: cell %d has no background", s, x)
			}
		}
	}
}

func TestCodeColoursAndCuts(t *testing.T) {
	th := testTheme()
	text := "func Do() error"
	out := th.Code(text, []Span{{0, 4, Keyword}}, Range{5, 7}, Add, false, 0, 20)
	row := cells(out, 20)
	kw := overlay.ReadableAt(solid(DefaultSyntax()[Keyword]), th.Bg(Add, false, false), floorCode)
	if !same(row[0].Style.Fg, kw) {
		t.Errorf("the keyword is in %v, want %v", row[0].Style.Fg, kw)
	}
	if !same(row[5].Style.Bg, th.Bg(Add, false, true)) || !same(row[0].Style.Bg, th.Bg(Add, false, false)) {
		t.Error("the changed part is not on its own ground")
	}
	if !same(row[19].Style.Bg, th.Bg(Add, false, false)) {
		t.Error("the padding is not on the line's ground")
	}
	if got := ansi.Strip(th.Code(text, nil, Range{}, Context, false, 0, 8)); got != "func Do"+overlay.Ellipsis() {
		t.Errorf("a long line cut to %q", got)
	}
	if got := ansi.Strip(th.Code(text, nil, Range{}, Context, false, 5, 6)); got != "Do() "+overlay.Ellipsis() {
		t.Errorf("a line scrolled by 5 shows %q", got)
	}
	if got := ansi.Strip(th.Sign(Add, false, true)); got != "+"+overlay.Ellipsis() {
		t.Errorf("a scrolled sign is %q", got)
	}
}

func TestThemeReadsOnLightAndDarkGrounds(t *testing.T) {
	for _, ground := range []color.Color{color.RGBA{0xff, 0xff, 0xff, 0xff}, color.RGBA{0x10, 0x10, 0x10, 0xff}} {
		// Colours picked for the other end, so every one of them has to be
		// lifted to read.
		th := NewTheme(Palette{Ground: ground, Fg: ground, Syntax: SyntaxFromANSI([16]color.Color{5: ground, 2: ground})})
		for _, kind := range []Kind{Context, Add, Delete} {
			bg := th.Bg(kind, false, false)
			out := th.Code("x", []Span{{0, 1, Keyword}}, Range{}, kind, false, 0, 1)
			fg := cells(out, 1)[0].Style.Fg
			if r := overlay.ContrastRatio(fg, bg); r < overlay.ContrastFloor-0.01 {
				t.Errorf("ground %v kind %d: a keyword measures %.2f:1", ground, kind, r)
			}
		}
	}
}

func same(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == b
	}
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	return ar>>8 == br>>8 && ag>>8 == bg>>8 && ab>>8 == bb>>8
}

func BenchmarkHighlight5000Lines(b *testing.B) {
	lines := make([]string, 5000)
	for i := range lines {
		lines[i] = "\tif err := f(ctx, \"value\", 42); err != nil { return err } // check"
	}
	b.ResetTimer()
	for range b.N {
		Highlight("x.go", lines)
	}
}
