package vt

import (
	"fmt"
	"image/color"
	"math/rand"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/fuzz/vtgen"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// scrollbackByCells is the scrollback text the slow way: every line decoded
// into cells and turned back into text. AppendScrollbackText has to produce
// exactly this.
func scrollbackByCells(e *Emulator) string {
	var sb strings.Builder
	for i := range e.ScrollbackLen() {
		sb.WriteString(e.ScrollbackLine(i).String())
		sb.WriteByte('\n')
	}
	return sb.String()
}

func scrollbackByText(e *Emulator) string {
	var sb strings.Builder
	e.AppendScrollbackText(&sb)
	return sb.String()
}

// ringByCells and ringByText are the same pair for a bare ring.
func ringByCells(sb *Scrollback) string {
	var out strings.Builder
	for i := range sb.Len() {
		out.WriteString(sb.Line(i).String())
		out.WriteByte('\n')
	}
	return out.String()
}

func ringByText(sb *Scrollback) string {
	var out strings.Builder
	for i := range sb.Len() {
		sb.appendLineText(&out, sb.lines[sb.slot(i)])
		out.WriteByte('\n')
	}
	return out.String()
}

// firstDifference describes where two captures part, for a failure message a
// person can act on.
func firstDifference(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := range max(len(wl), len(gl)) {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			return fmt.Sprintf("line %d: by cells %q, by text %q", i, w, g)
		}
	}
	return "no difference"
}

// TestScrollbackTextMatchesLineStringOnTerminalOutput feeds the emulator lines
// with every kind of cell a program prints and requires the direct text path
// to write what decoding each line into cells writes.
func TestScrollbackTextMatchesLineStringOnTerminalOutput(t *testing.T) {
	inputs := []string{
		"plain line\r\n",
		"\x1b[31mred\x1b[0m and \x1b[1;44mbold on blue   \x1b[0m tail   \r\n",
		"wide 日本語 text\r\n",
		"emoji 👍🏽 and flag 🇯🇵\r\n",
		"\x1b]8;;http://x.y\x1b\\link\x1b]8;;\x1b\\ after\r\n",
		"\x1b]8;;http://x.y\x1b\\   \x1b]8;;\x1b\\spaces in a link\r\n",
		"\x1b[44m          \x1b[0m\r\n",
		"tabs\tand\tmore\r\n",
		"   leading spaces\r\n",
		"\x1b[7m \x1b[0m\r\n",
		"combining é café\r\n",
		"\r\n",
		"\r\n\r\n",
		"trailing spaces then styled \x1b[41m \x1b[0m\r\n",
		"\x1b[4:3m\x1b[58;2;1;2;3mcurly underline\x1b[0m\r\n",
		"\x1b[38;2;10;20;30mtruecolor\x1b[0m\r\n",
		"a\x1b[5Cgap\r\n",
		"wide at the edge 漢字漢字漢字漢字漢字漢字漢字漢字漢字漢字漢字漢字漢字漢字漢字漢字漢字漢字漢字漢字漢字漢字\r\n",
		"zero\u200bwidth\r\n",
		"\x1b[2mdim\x1b[0m\x1b[9mstrike\x1b[0m\r\n",
	}
	for _, width := range []int{10, 40, 80} {
		e := NewEmulator(width, 5)
		e.SetScrollbackMaxLines(1000)
		for round := range 30 {
			for _, in := range inputs {
				fmt.Fprintf(e, "%d ", round)
				_, _ = e.WriteString(in)
			}
		}
		if e.ScrollbackLen() == 0 {
			t.Fatalf("width %d: nothing reached the scrollback", width)
		}
		if want, got := scrollbackByCells(e), scrollbackByText(e); want != got {
			t.Fatalf("width %d: %s", width, firstDifference(want, got))
		}
	}
}

// TestScrollbackTextOfHandBuiltLines pins the cells whose text is easy to get
// wrong one at a time: the zero cell, a wide rune's spacer, a painted blank, a
// blank inside a link, and a line with nothing on it.
func TestScrollbackTextOfHandBuiltLines(t *testing.T) {
	blank := func(width int) uv.Line {
		line := make(uv.Line, width)
		for x := range line {
			line[x] = uv.EmptyCell
		}
		return line
	}
	red := uv.Style{Bg: ansi.BasicColor(1)}
	link := uv.Link{URL: "https://example.test"}
	cases := []struct {
		name  string
		build func() uv.Line
		want  string
	}{
		{"every kind of cell", func() uv.Line { return styledLine(20) }, ""},
		{"empty line", func() uv.Line { return blank(10) }, ""},
		{"zero-width line", func() uv.Line { return uv.Line{} }, ""},
		{"zero cells between letters", func() uv.Line {
			l := blank(8)
			l[0] = uv.Cell{Content: "a", Width: 1}
			l[1] = uv.Cell{}
			l[2] = uv.Cell{}
			l[3] = uv.Cell{Content: "b", Width: 1}
			return l
		}, "ab"},
		{"zero cell between blanks adds nothing", func() uv.Line {
			l := blank(8)
			l[0] = uv.Cell{Content: "a", Width: 1}
			l[2] = uv.Cell{}
			l[4] = uv.Cell{Content: "b", Width: 1}
			return l
		}, "a  b"},
		{"wide runes and their spacers", func() uv.Line {
			l := blank(8)
			l[0] = uv.Cell{Content: "漢", Width: 2}
			l[1] = uv.Cell{Content: "", Width: 0}
			l[3] = uv.Cell{Content: "字", Width: 2}
			l[4] = uv.Cell{Content: "", Width: 0}
			return l
		}, "漢 字"},
		{"styled spacer is not skipped", func() uv.Line {
			l := blank(6)
			l[0] = uv.Cell{Content: "漢", Width: 2, Style: red}
			l[1] = uv.Cell{Content: "", Width: 0, Style: red}
			l[3] = uv.Cell{Content: "x", Width: 1}
			return l
		}, "漢 x"},
		{"styled cell of width 0 releases pending blanks", func() uv.Line {
			l := blank(6)
			l[0] = uv.Cell{Content: "a", Width: 1}
			l[3] = uv.Cell{Content: "", Width: 0, Style: red}
			return l
		}, "a  "},
		{"painted blanks are text", func() uv.Line {
			l := blank(8)
			l[0] = uv.Cell{Content: "a", Width: 1}
			l[2] = uv.Cell{Content: " ", Width: 1, Style: red}
			l[3] = uv.Cell{Content: " ", Width: 1, Style: red}
			return l
		}, "a   "},
		{"blank inside a link is text", func() uv.Line {
			l := blank(8)
			l[0] = uv.Cell{Content: "a", Width: 1, Link: link}
			l[1] = uv.Cell{Content: " ", Width: 1, Link: link}
			l[2] = uv.Cell{Content: " ", Width: 1, Link: link}
			return l
		}, "a  "},
		{"plain blanks after a link are held back", func() uv.Line {
			l := blank(8)
			l[0] = uv.Cell{Content: "a", Width: 1, Link: link}
			l[1] = uv.Cell{Content: " ", Width: 1}
			l[2] = uv.Cell{Content: " ", Width: 1}
			l[3] = uv.Cell{Content: "b", Width: 1}
			return l
		}, "a  b"},
		{"empty content of width one", func() uv.Line {
			l := blank(6)
			l[1] = uv.Cell{Content: "", Width: 1}
			l[2] = uv.Cell{Content: "c", Width: 1}
			return l
		}, " c"},
		{"grapheme clusters", func() uv.Line {
			l := blank(8)
			l[0] = uv.Cell{Content: "🇬🇧", Width: 2}
			l[1] = uv.Cell{Content: "", Width: 0}
			l[2] = uv.Cell{Content: "é", Width: 1}
			l[3] = uv.Cell{Content: "👍🏽", Width: 2}
			l[4] = uv.Cell{Content: "", Width: 0}
			return l
		}, "🇬🇧é👍🏽"},
		{"a colour only RGBA can compare", func() uv.Line {
			l := blank(6)
			l[0] = uv.Cell{Content: " ", Width: 1, Style: uv.Style{Fg: oddColor{0}}}
			l[1] = uv.Cell{Content: "x", Width: 1, Style: uv.Style{Bg: color.RGBA{A: 255}}}
			return l
		}, " x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			line := tc.build()
			sb := NewScrollback(4)
			sb.PushLine(line)
			if sb.Len() == 0 {
				sb.PushBlankLine(len(line))
			}
			want := ringByCells(sb)
			if got := ringByText(sb); got != want {
				t.Fatalf("by cells %q, by text %q", want, got)
			}
			if tc.want != "" && want != tc.want+"\n" {
				t.Fatalf("the line reads %q, the case expects %q", want, tc.want+"\n")
			}
		})
	}
}

// TestScrollbackTextFollowsTheRing wraps a small ring many times, with lines of
// changing width, and checks the text comes out oldest first after every push.
func TestScrollbackTextFollowsTheRing(t *testing.T) {
	const ring = 7
	sb := NewScrollback(ring)
	for n := range 40 {
		width := 3 + n%11
		line := make(uv.Line, width)
		for x := range line {
			line[x] = uv.EmptyCell
		}
		for x, r := range fmt.Sprintf("%d", n) {
			if x < width {
				line[x] = uv.Cell{Content: string(r), Width: 1}
			}
		}
		if n%5 == 0 {
			sb.PushBlankLine(width)
		} else {
			sb.PushLine(line)
		}
		want := ringByCells(sb)
		got := ringByText(sb)
		if got != want {
			t.Fatalf("after push %d: %s", n, firstDifference(want, got))
		}
		if lines := strings.Count(got, "\n"); lines != min(n+1, ring) {
			t.Fatalf("after push %d: %d lines, want %d", n, lines, min(n+1, ring))
		}
	}
	if first := strings.SplitN(ringByText(sb), "\n", 2)[0]; first != "33" {
		t.Fatalf("oldest line after wrapping is %q, want %q", first, "33")
	}
}

// TestScrollbackTextOfRandomLines pushes random mixes of every cell kind
// through a ring that wraps, and holds the two paths to each other on every
// line, whole and cut short at every byte. The decoder stops at a token it
// cannot read whole, so the text path has to stop at the same place.
func TestScrollbackTextOfRandomLines(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	colors := []color.Color{nil, nil, nil, ansi.BasicColor(1), ansi.IndexedColor(99), ansi.RGBColor{R: 0xab, G: 0xcd, B: 0xef},
		color.RGBA{R: 1, G: 2, B: 3, A: 255}, color.RGBA{R: 4, G: 5, B: 6, A: 7}, oddColor{3}, oddColor{0}}
	links := []uv.Link{{}, {}, {URL: "https://a.test"}, {URL: "https://b.test", Params: "id=2"}}
	contents := []string{" ", " ", " ", "a", "Z", "é", "漢", "🇬🇧", "é", "�", "\x00", "\xff", "", "ab"}
	randomCell := func() uv.Cell {
		c := uv.Cell{Content: contents[rng.Intn(len(contents))], Width: 1}
		switch c.Content {
		case "漢", "🇬🇧":
			c.Width = 2
		case "":
			c.Width = rng.Intn(2)
		}
		if rng.Intn(6) == 0 {
			c.Width = rng.Intn(4)
		}
		if rng.Intn(3) == 0 {
			c.Style = uv.Style{
				Fg: colors[rng.Intn(len(colors))], Bg: colors[rng.Intn(len(colors))],
				UnderlineColor: colors[rng.Intn(len(colors))],
				Underline:      uv.Underline(rng.Intn(3)), Attrs: uint8(rng.Intn(2)),
			}
		}
		if rng.Intn(5) == 0 {
			c.Link = links[rng.Intn(len(links))]
		}
		return c
	}

	sb := NewScrollback(29)
	for range 1500 {
		width := 1 + rng.Intn(40)
		line := make(uv.Line, width)
		for x := range line {
			line[x] = uv.EmptyCell
		}
		for x := range rng.Intn(width + 1) {
			line[x] = randomCell()
		}
		sb.PushLine(line)

		data := sb.lines[sb.slot(sb.Len()-1)]
		for cut := 0; cut <= len(data); cut++ {
			want := sb.decodeLine(data[:cut]).String()
			var got strings.Builder
			sb.appendLineText(&got, data[:cut])
			if got.String() != want {
				t.Fatalf("record %x cut at %d of %d: by cells %q, by text %q", data, cut, len(data), want, got.String())
			}
		}
	}
	if want, got := ringByCells(sb), ringByText(sb); want != got {
		t.Fatal(firstDifference(want, got))
	}
}

// TestScrollbackTextUnderGeneratedInput drives the emulator with generated
// terminal input, resizes included, into a ring small enough to wrap, and
// compares the two paths after each script.
func TestScrollbackTextUnderGeneratedInput(t *testing.T) {
	seeds := uint64(300)
	if testing.Short() {
		seeds = 60
	}
	for seed := range seeds {
		e := NewEmulator(1+int(seed%40), 1+int(seed%7))
		e.SetScrollbackMaxLines(50)
		for _, seq := range vtgen.New(seed).Script(200) {
			if seq.Kind == "resize" {
				e.Resize(seq.Cols, seq.Rows)
				continue
			}
			_, _ = e.WriteString(seq.Bytes)
			_, _ = e.WriteString("\r\n")
		}
		if want, got := scrollbackByCells(e), scrollbackByText(e); want != got {
			t.Fatalf("seed %d: %s", seed, firstDifference(want, got))
		}
	}
}

// TestScrollbackTextWithoutARing covers a screen with no scrollback at all.
func TestScrollbackTextWithoutARing(t *testing.T) {
	e := NewEmulator(10, 2)
	_, _ = e.WriteString("a\r\nb\r\nc\r\n")
	e.scrs[0].DisableScrollback()
	if got := scrollbackByText(e); got != "" {
		t.Fatalf("no ring wrote %q", got)
	}
}

// TestReadContentDoesNotAllocateForASCII pins the table lookup: an ASCII cell
// read back from the ring costs no allocation.
func TestReadContentDoesNotAllocateForASCII(t *testing.T) {
	sb := NewScrollback(1)
	data := []byte("x")
	allocs := testing.AllocsPerRun(100, func() {
		if s, _, ok := sb.readContent(data, 0); !ok || s != "x" {
			t.Fatalf("read %q, %v", s, ok)
		}
	})
	if allocs != 0 {
		t.Fatalf("reading an ASCII cell allocates %v times", allocs)
	}
}
