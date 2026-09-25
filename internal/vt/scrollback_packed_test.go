package vt

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image/color"
	"math/rand"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// oddColor is a colour type the packer does not know, so it has to intern.
type oddColor struct{ v uint8 }

func (c oddColor) RGBA() (r, g, b, a uint32) {
	return uint32(c.v) * 0x101, 0, 0, 0xffff
}

// TestPlainLineCostsItsTextInBytes pins the point of the encoding: a line of
// plain text is stored as that text, one byte per ASCII cell plus the width
// header, not 24 bytes per cell.
func TestPlainLineCostsItsTextInBytes(t *testing.T) {
	const width = 207
	text := "compiling package github.com/example/project/internal/thing"
	line := make(uv.Line, width)
	for i := range line {
		line[i] = uv.EmptyCell
	}
	for i, r := range text {
		line[i] = uv.Cell{Content: string(r), Width: 1}
	}
	sb := NewScrollback(4)
	sb.PushLine(line)
	if got, want := len(sb.lines[0]), len(text)+2; got != want {
		t.Fatalf("a %d-character plain line is stored in %d bytes, want %d (text plus a two-byte width)", len(text), got, want)
	}
}

// styledLine is one of everything a cell can carry, followed by blanks.
func styledLine(width int) uv.Line {
	line := make(uv.Line, width)
	for i := range line {
		line[i] = uv.EmptyCell
	}
	line[0] = uv.Cell{Content: "a", Width: 1}
	line[1] = uv.Cell{Content: "漢", Width: 2}
	line[2] = uv.Cell{Content: "", Width: 0} // the wide rune's spacer
	line[3] = uv.Cell{Content: "é", Width: 1}
	line[4] = uv.Cell{Content: "🇬🇧", Width: 2}
	line[5] = uv.Cell{Content: "", Width: 0}
	line[6] = uv.Cell{Content: "b", Width: 1, Style: uv.Style{Fg: ansi.BasicColor(3), Bg: ansi.IndexedColor(200)}}
	line[7] = uv.Cell{Content: "c", Width: 1, Style: uv.Style{Fg: ansi.RGBColor{R: 0x12, G: 0x34, B: 0x56}, UnderlineColor: color.RGBA{R: 1, G: 2, B: 3, A: 255}, Underline: uv.UnderlineCurly, Attrs: uv.AttrBold | uv.AttrItalic}}
	line[8] = uv.Cell{Content: "d", Width: 1, Style: uv.Style{Bg: color.RGBA{R: 9, G: 8, B: 7, A: 128}}}
	line[9] = uv.Cell{Content: "e", Width: 1, Style: uv.Style{Fg: oddColor{7}}}
	line[10] = uv.Cell{Content: "f", Width: 1, Link: uv.Link{URL: "https://example.test", Params: "id=1"}}
	line[11] = uv.Cell{Content: " ", Width: 1, Style: uv.Style{Bg: ansi.BasicColor(1)}} // a painted blank
	line[12] = uv.Cell{Content: "�", Width: 1}
	line[13] = uv.Cell{Content: "\x00", Width: 1}
	return line
}

func TestScrollbackRoundTripsEveryKindOfCell(t *testing.T) {
	const width = 40
	want := styledLine(width)
	sb := NewScrollback(4)
	sb.PushLine(want)

	got := sb.Line(0)
	if len(got) != width {
		t.Fatalf("line came back %d wide, want %d", len(got), width)
	}
	for x := range width {
		if !reflect.DeepEqual(got[x], want[x]) {
			t.Errorf("cell %d: got %#v, want %#v", x, got[x], want[x])
		}
	}
	if got, ok := storedCellWidth(sb.lines[0], 13); !ok || got != 1 {
		t.Errorf("cell 13 is stored with width %d (%v), want 1", got, ok)
	}
	if _, ok := storedCellWidth(sb.lines[0], 14); ok {
		t.Errorf("cell 14 is stored: the blank tail is not stored")
	}
	if w, _ := binary.Uvarint(sb.lines[0]); int(w) != width {
		t.Errorf("stored width %d, want %d", w, width)
	}
}

func TestScrollbackLineCacheFollowsTheRing(t *testing.T) {
	sb := NewScrollback(2)
	push := func(s string) {
		sb.PushLine(uv.Line{{Content: s, Width: 1}, uv.EmptyCell})
	}
	push("A")
	if got := sb.Line(0)[0].Content; got != "A" {
		t.Fatalf("line 0 is %q, want A", got)
	}
	push("B")
	push("C") // evicts A
	if got := sb.Line(0)[0].Content; got != "B" {
		t.Fatalf("line 0 is %q after the ring moved, want B", got)
	}
	if got := sb.Line(1)[0].Content; got != "C" {
		t.Fatalf("line 1 is %q, want C", got)
	}
	// Same index, same generation: the decoded line is shared.
	if a, b := sb.Line(0), sb.Line(0); &a[0] != &b[0] {
		t.Fatal("two reads of one line in one generation decoded twice")
	}
}

func TestScrollbackCacheIsBounded(t *testing.T) {
	sb := NewScrollback(cacheCap * 2)
	for i := range cacheCap * 2 {
		sb.PushLine(uv.Line{{Content: string(rune('a' + i%26)), Width: 1}})
	}
	for i := range cacheCap * 2 {
		_ = sb.Line(i)
	}
	if n := len(sb.cache); n > cacheCap {
		t.Fatalf("cache holds %d decoded lines after a walk of the ring, want at most %d", n, cacheCap)
	}
}

func TestScrollbackFullRingReusesEvictedStorage(t *testing.T) {
	sb := NewScrollback(8)
	line := uv.Line{{Content: "x", Width: 1}, {Content: "y", Width: 1}, uv.EmptyCell, uv.EmptyCell}
	for range 8 {
		sb.PushLine(line)
	}
	if got := testing.AllocsPerRun(100, func() { sb.PushLine(line) }); got > 0 {
		t.Fatalf("a push into a full ring allocates %.1f times, want 0", got)
	}
}

func TestBlankWideRunesCutByTheEdgeOnPackedLines(t *testing.T) {
	sb := NewScrollback(4)
	sb.PushLine(uv.Line{{Content: "a", Width: 1}, {Content: "漢", Width: 2, Style: uv.Style{Fg: ansi.BasicColor(2)}}, {Content: "", Width: 0}})
	sb.blankWideRunesCutByTheEdge(2)
	got := sb.Line(0)[1]
	if got.Content != " " || got.Width != 1 || got.Style.Fg != ansi.BasicColor(2) {
		t.Fatalf("cut cell is %#v, want a styled blank", got)
	}
}

// blankWideRunesCutByTheEdgeReference is blankWideRunesCutByTheEdge without
// its skip of lines that hold no sbCell byte: every line is walked to x.
func (sb *Scrollback) blankWideRunesCutByTheEdgeReference(newWidth int) {
	x := newWidth - 1
	if x < 0 {
		return
	}
	for i := range sb.Len() {
		slot := sb.slot(i)
		w, ok := storedCellWidth(sb.lines[slot], x)
		if !ok || w <= 1 {
			continue
		}
		line := sb.decodeLine(sb.lines[slot])
		line[x].Content = " "
		line[x].Width = 1
		line[x].Link = uv.Link{}
		n := len(line)
		for n > 0 && isBlankCell(&line[n-1]) {
			n--
		}
		sb.lines[slot] = sb.encodeLine(sb.lines[slot][:0], line[:n], len(line))
	}
}

// TestBlankWideRunesCutByTheEdgeSkipsOnlyNarrowLines holds the resize path,
// which does not walk a line without an sbCell byte, to the full walk. The
// skip is only right while every wide cell is stored behind sbCell, so this
// fails if an encoding change ever stores one some other way. The lines mix
// wide and zero-width cells, graphemes, links and styles over indexed
// colours whose uvarints contain the sbCell byte.
func TestBlankWideRunesCutByTheEdgeSkipsOnlyNarrowLines(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	colors := []color.Color{nil, ansi.BasicColor(3), ansi.IndexedColor(0xFD), ansi.RGBColor{R: 0xfd, G: 0xfd, B: 0xfd}, oddColor{0xFD}}
	contents := []string{"a", "é", "漢", "🇬🇧", "", "e\u0301", "─", " "}
	for round := range 200 {
		const ring = 30
		got, want := NewScrollback(ring), NewScrollback(ring)
		for range 40 {
			width := 1 + rng.Intn(40)
			line := make(uv.Line, width)
			for x := range line {
				line[x] = uv.EmptyCell
			}
			for x := range rng.Intn(width + 1) {
				c := uv.Cell{Content: contents[rng.Intn(len(contents))], Width: 1}
				switch c.Content {
				case "漢", "🇬🇧":
					c.Width = 2
				case "":
					c.Width = 0
				}
				if rng.Intn(3) == 0 {
					c.Style = uv.Style{Fg: colors[rng.Intn(len(colors))], Bg: colors[rng.Intn(len(colors))]}
				}
				if rng.Intn(6) == 0 {
					c.Link = uv.Link{URL: "https://x.test/\xfd"}
				}
				line[x] = c
			}
			got.PushLine(line)
			want.PushLine(line)
		}
		newWidth := 1 + rng.Intn(40)
		got.blankWideRunesCutByTheEdge(newWidth)
		want.blankWideRunesCutByTheEdgeReference(newWidth)
		for i := range want.Len() {
			if g, w := got.Line(i), want.Line(i); !reflect.DeepEqual(g, w) {
				t.Fatalf("round %d, width %d, line %d:\n got %#v\nwant %#v", round, newWidth, i, g, w)
			}
		}
	}
}

// TestPackedScrollbackHoldsALineForItsContent is the reason for the packing:
// a thousand short lines on a wide terminal used to cost width times 112
// bytes each, 23 MB at 207 columns; packed and trimmed they cost about the
// five cells that are on them.
func TestPackedScrollbackHoldsALineForItsContent(t *testing.T) {
	const width, lines = 207, 1000
	line := make(uv.Line, width)
	for i := range line {
		line[i] = uv.EmptyCell
	}
	for i, r := range "12345" {
		line[i] = uv.Cell{Content: string(r), Width: 1}
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	sb := NewScrollback(lines)
	for range lines {
		sb.PushLine(line)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(sb)

	held := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	const unpacked = width * 112 * lines
	if held > unpacked/10 {
		t.Fatalf("%d lines of %d columns hold %d bytes, want under a tenth of the %d the unpacked cells took", lines, width, held, unpacked)
	}
	t.Logf("%d lines x %d columns, five cells each: %d KiB packed, %d KiB unpacked", lines, width, held/1024, unpacked/1024)
}

// TestScrollbackRoundTripsRandomLines pushes lines built from every kind of
// cell in random order, through a ring that wraps, and reads each back. The
// one-of-each line above pins the encoding of each cell; this pins that the
// style and link runs the encoding shares between cells come back on the
// right cells whatever the neighbours are.
func TestScrollbackRoundTripsRandomLines(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	colors := []color.Color{nil, ansi.BasicColor(1), ansi.IndexedColor(99), ansi.RGBColor{R: 0xab, G: 0xcd, B: 0xef},
		color.RGBA{R: 1, G: 2, B: 3, A: 255}, color.RGBA{R: 4, G: 5, B: 6, A: 7}, oddColor{3}, oddColor{200}}
	links := []uv.Link{{}, {URL: "https://a.test"}, {URL: "https://b.test", Params: "id=2"}}
	contents := []string{" ", "a", "Z", "é", "漢", "🇬🇧", "é", "�", "\x00", "\xff", "", "ab"}
	randomCell := func() uv.Cell {
		c := uv.Cell{Content: contents[rng.Intn(len(contents))], Width: 1}
		switch c.Content {
		case "漢", "🇬🇧":
			c.Width = 2
		case "":
			c.Width = 0
		}
		if rng.Intn(4) == 0 {
			c.Width = rng.Intn(4)
		}
		if rng.Intn(3) == 0 {
			c.Style = uv.Style{
				Fg: colors[rng.Intn(len(colors))], Bg: colors[rng.Intn(len(colors))],
				UnderlineColor: colors[rng.Intn(len(colors))],
				Underline:      uv.Underline(rng.Intn(6)), Attrs: uint8(rng.Intn(256)),
			}
		}
		if rng.Intn(5) == 0 {
			c.Link = links[rng.Intn(len(links))]
		}
		return c
	}

	const ring = 37
	sb := NewScrollback(ring)
	var pushed []uv.Line
	for range 500 {
		width := 1 + rng.Intn(60)
		line := make(uv.Line, width)
		for x := range line {
			line[x] = uv.EmptyCell
		}
		filled := rng.Intn(width + 1)
		for x := range filled {
			line[x] = randomCell()
		}
		sb.PushLine(line)
		pushed = append(pushed, line)
		if len(pushed) > ring {
			pushed = pushed[1:]
		}
		if got := sb.Len(); got != len(pushed) {
			t.Fatalf("ring holds %d lines, want %d", got, len(pushed))
		}
		i := rng.Intn(len(pushed))
		got := sb.Line(i)
		if !reflect.DeepEqual(got, pushed[i]) {
			for x := range pushed[i] {
				if x < len(got) && !reflect.DeepEqual(got[x], pushed[i][x]) {
					t.Fatalf("line %d cell %d: got %#v, want %#v", i, x, got[x], pushed[i][x])
				}
			}
			t.Fatalf("line %d: got %d cells, want %d", i, len(got), len(pushed[i]))
		}
	}
}

// encodeLineReference is encodeLine without its plain-ASCII shortcut: every
// cell goes through the style, link, width and content tokens.
func (sb *Scrollback) encodeLineReference(buf []byte, cells uv.Line, width int) []byte {
	buf = binary.AppendUvarint(buf, uint64(width))
	var style packedStyle
	var link uv.Link
	sb.lineLinks = sb.lineLinks[:0]
	for i := range cells {
		c := &cells[i]
		if st := sb.packStyle(&c.Style); st != style {
			style = st
			buf = append(buf, sbStyle)
			buf = binary.AppendUvarint(buf, uint64(st.fg))
			buf = binary.AppendUvarint(buf, uint64(st.bg))
			buf = binary.AppendUvarint(buf, uint64(st.ul))
			buf = append(buf, st.attrs, st.underline)
		}
		if c.Link != link {
			link = c.Link
			buf = sb.appendLink(buf, link)
		}
		if c.Width != 1 {
			buf = append(buf, sbCell, uint8(max(0, min(c.Width, 255))))
		}
		buf = sb.appendContent(buf, c.Content)
	}
	return buf
}

// TestEncodeLinePlainShortcutWritesTheSameBytes holds encodeLine's shortcut for
// plain ASCII cells to the bytes the full path writes, on lines that mix plain
// runs with styled, linked, wide and non-ASCII cells in every order.
func TestEncodeLinePlainShortcutWritesTheSameBytes(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	styles := []uv.Style{{}, {Fg: ansi.BasicColor(1)}, {Bg: ansi.RGBColor{R: 0x12, G: 0x34, B: 0x56}},
		{Attrs: 1}, {Underline: 1}, {UnderlineColor: ansi.IndexedColor(7)}}
	links := []uv.Link{{}, {URL: "https://a.test"}, {Params: "id=1"}}
	contents := []string{"a", "b", " ", "~", "\x00", "\x7f", "é", "漢", "", "ab", "\xff"}
	sb := NewScrollback(16)
	for round := range 2000 {
		line := make(uv.Line, rng.Intn(40))
		for x := range line {
			c := uv.Cell{Content: contents[rng.Intn(len(contents))], Width: 1}
			if rng.Intn(4) == 0 {
				c.Width = rng.Intn(3)
			}
			if rng.Intn(3) == 0 {
				c.Style = styles[rng.Intn(len(styles))]
			}
			if rng.Intn(5) == 0 {
				c.Link = links[rng.Intn(len(links))]
			}
			line[x] = c
		}
		got := sb.encodeLine(nil, line, len(line))
		want := sb.encodeLineReference(nil, line, len(line))
		if !bytes.Equal(got, want) {
			t.Fatalf("round %d: line %#v\n got  %x\n want %x", round, line, got, want)
		}
	}
}

// TestScrollbackRingGrowsAsLinesArrive pins that an empty ring holds no
// line headers: at the default depth the headers alone were 320 KB per pane
// before the pane had printed anything, on each side of the socket.
func TestScrollbackRingGrowsAsLinesArrive(t *testing.T) {
	sb := NewScrollback(10000)
	if sb.lines != nil {
		t.Fatalf("a new ring holds %d line slots, want none", cap(sb.lines))
	}
	line := uv.Line{{Content: "x", Width: 1}}
	for range 100 {
		sb.PushLine(line)
	}
	if cap(sb.lines) > 1000 {
		t.Fatalf("after 100 lines the ring has room for %d, want it to grow with use", cap(sb.lines))
	}
	for range 10000 {
		sb.PushLine(line)
	}
	if got := sb.Len(); got != 10000 {
		t.Fatalf("ring holds %d lines, want 10000", got)
	}
	if got := cap(sb.lines); got != 10000 {
		t.Fatalf("a full ring has room for %d lines, want exactly 10000", got)
	}
	if got := sb.Line(0)[0].Content; got != "x" {
		t.Fatalf("oldest line is %q, want x", got)
	}
}

// TestScrollbackForgetsWhatItEvicts holds a full ring to its size when every
// line carries something new: a hyperlink to a different file and a
// multi-rune cluster, which is what an agent or a compiler printing
// file:line links emits. Links and clusters used to be interned for the life
// of the pane, so the ring let go of its lines and kept everything they
// pointed at, about 150 bytes a link.
func TestScrollbackForgetsWhatItEvicts(t *testing.T) {
	const ring, pushed = 1000, 50000
	url := func(i int) string { return fmt.Sprintf("file:///home/user/project/pkg/file_%d.go", i) }
	line := make(uv.Line, 80)
	sb := NewScrollback(ring)
	push := func(i int) {
		for x := range line {
			line[x] = uv.EmptyCell
		}
		link := uv.Link{URL: url(i)}
		for x, r := range fmt.Sprintf("pkg/file_%d.go:1", i) {
			line[x] = uv.Cell{Content: string(r), Width: 1, Link: link}
		}
		line[len(line)-2] = uv.Cell{Content: fmt.Sprintf("é%c", 'a'+rune(i%26)), Width: 1}
		sb.PushLine(line)
	}
	for i := range ring {
		push(i)
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for i := ring; i < pushed; i++ {
		push(i)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(sb)
	if grew := int64(after.HeapAlloc) - int64(before.HeapAlloc); grew > 256<<10 {
		t.Fatalf("a full ring of %d lines grew by %d bytes over %d more pushes, want it flat", ring, grew, pushed-ring)
	}
	if got := sb.Line(ring - 1)[0].Link.URL; got != url(pushed-1) {
		t.Fatalf("newest line links to %q, want %q", got, url(pushed-1))
	}
}

// TestScrollbackWritesALinkOncePerLine checks that a line going in and out of
// the same link writes the link's text once and refers back to it after,
// and that the cells of one link decode sharing its strings.
func TestScrollbackWritesALinkOncePerLine(t *testing.T) {
	long := uv.Link{URL: "https://example.test/" + strings.Repeat("p", 500), Params: "id=1"}
	other := uv.Link{URL: "https://other.test/"}
	line := make(uv.Line, 60)
	for x := range line {
		line[x] = uv.Cell{Content: "x", Width: 1}
		switch x % 3 {
		case 0:
			line[x].Link = long
		case 1:
			line[x].Link = other
		}
	}
	sb := NewScrollback(4)
	sb.PushLine(line)
	if stored := len(sb.lines[0]); stored > len(long.URL)+len(other.URL)+300 {
		t.Errorf("line stores %d bytes, want each link's text written once", stored)
	}
	got := sb.Line(0)
	if !reflect.DeepEqual(got, line) {
		t.Fatalf("line did not round trip:\n got %#v\nwant %#v", got, line)
	}
	if unsafe.StringData(got[0].Link.URL) != unsafe.StringData(got[57].Link.URL) {
		t.Error("the cells of one link decode to separate copies of its URL")
	}
}

// TestScrollbackDropsAnOversizedLink bounds what one link costs the ring. A
// line writes each link it uses, so a guest leaving a link of megabytes open
// would otherwise store a copy with every line it printed after.
func TestScrollbackDropsAnOversizedLink(t *testing.T) {
	for _, tc := range []struct {
		link uv.Link
		kept bool
	}{
		{uv.Link{URL: strings.Repeat("u", maxStoredLinkURL)}, true},
		{uv.Link{URL: strings.Repeat("u", maxStoredLinkURL+1)}, false},
		{uv.Link{URL: "u", Params: strings.Repeat("p", maxStoredLinkParams)}, true},
		{uv.Link{URL: "u", Params: strings.Repeat("p", maxStoredLinkParams+1)}, false},
	} {
		sb := NewScrollback(4)
		sb.PushLine(uv.Line{{Content: "a", Width: 1, Link: tc.link}, {Content: "b", Width: 1, Link: tc.link}, {Content: "c", Width: 1}})
		got := sb.Line(0)
		for x, c := range got {
			want := tc.link
			if !tc.kept || x == 2 {
				want = uv.Link{}
			}
			if c.Link != want || c.Content != string(rune('a'+x)) {
				t.Errorf("URL %d, params %d bytes: cell %d is %q with a %d-byte link, want %d", len(tc.link.URL), len(tc.link.Params), x, c.Content, len(c.Link.URL), len(want.URL))
			}
		}
	}
}

// TestScrollbackDecodesEveryTruncationSafely cuts a stored line holding links,
// back references and clusters at every byte, and decodes and walks each
// prefix. A record cut short has to stop the decoder, not index past it.
func TestScrollbackDecodesEveryTruncationSafely(t *testing.T) {
	a, b := uv.Link{URL: "https://a.test/", Params: "id=a"}, uv.Link{URL: "https://b.test/"}
	line := uv.Line{
		{Content: "x", Width: 1, Link: a}, {Content: "é", Width: 1, Link: b},
		{Content: "漢", Width: 2, Link: a}, {Content: "", Width: 0, Link: a},
		{Content: "\xff", Width: 1}, {Content: "👍🏽", Width: 2, Link: b}, {Content: "", Width: 0, Link: b},
	}
	sb := NewScrollback(4)
	sb.PushLine(line)
	whole := sb.lines[0]
	if got := sb.decodeLine(whole); !reflect.DeepEqual(got, line) {
		t.Fatalf("line did not round trip:\n got %#v\nwant %#v", got, line)
	}
	for n := range len(whole) {
		cut := whole[:n]
		_ = sb.decodeLine(cut)
		for x := range len(line) + 1 {
			_, _ = storedCellWidth(cut, x)
		}
	}
}
