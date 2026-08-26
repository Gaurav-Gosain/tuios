package shot

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// testGrid builds a small grid with one styled word on it.
func testGrid() *Grid {
	g := NewGrid(12, 3, RGB(0xcd, 0xd6, 0xf4), RGB(0x1e, 0x1e, 0x2e))
	put(g, 0, 0, "hello", func(c *Cell) { c.FG = RGB(0xa6, 0xe3, 0xa1) })
	put(g, 0, 1, "│─╭╯", nil)
	return g
}

func put(g *Grid, x, y int, s string, mod func(*Cell)) {
	for _, r := range s {
		if x >= g.Cols {
			return
		}
		c := &g.Cells[y][x]
		c.Cluster = string(r)
		c.Width = 1
		if mod != nil {
			mod(c)
		}
		x++
	}
}

func testFrame() *Frame {
	return &Frame{
		Mode: FrameWindow, Padding: 20, Radius: 8, Shadow: true,
		Controls: ControlsMacOS, Title: "demo",
		Accent:    RGB(0xcb, 0xa6, 0xf7),
		WashStart: RGB(0x30, 0x30, 0x50), WashEnd: RGB(0x20, 0x28, 0x40),
		FontFamily: "JetBrains Mono, monospace", Scale: 2,
	}
}

// TestRenderProducesEveryFormat checks that all five backends return
// non-empty, well-formed bytes for the same grid.
//
// Negative control: deleting the FormatHTML arm of Render's switch made this
// fail with `html: unknown format "html"`. Confirmed on the unfixed tree.
func TestRenderProducesEveryFormat(t *testing.T) {
	g, f := testGrid(), testFrame()
	for _, tc := range []struct {
		format Format
		check  func(t *testing.T, b []byte)
	}{
		{FormatSVG, func(t *testing.T, b []byte) {
			s := string(b)
			if !strings.HasPrefix(s, "<svg ") || !strings.HasSuffix(s, "</svg>\n") {
				t.Errorf("svg is not a complete document: %.60q...", s)
			}
			if strings.Contains(s, "foreignObject") {
				t.Error("svg used foreignObject, which the design forbids")
			}
			if !strings.Contains(s, ">hello<") {
				t.Error("svg lost the content")
			}
		}},
		{FormatPNG, func(t *testing.T, b []byte) {
			if _, err := png.Decode(bytes.NewReader(b)); err != nil {
				t.Errorf("png does not decode: %v", err)
			}
		}},
		{FormatANSI, func(t *testing.T, b []byte) {
			if !bytes.Contains(b, []byte("hello")) {
				t.Error("ansi lost the content")
			}
			if !bytes.Contains(b, []byte("\x1b[38;2;166;227;161m")) {
				t.Error("ansi lost the truecolor foreground")
			}
		}},
		{FormatHTML, func(t *testing.T, b []byte) {
			s := string(b)
			if !strings.HasPrefix(s, "<!doctype html>") {
				t.Error("html is not a standalone document")
			}
			if !strings.Contains(s, "hello") {
				t.Error("html lost the content")
			}
		}},
		{FormatText, func(t *testing.T, b []byte) {
			if string(b) != "hello\n│─╭╯\n\n" {
				t.Errorf("text is %q", string(b))
			}
		}},
	} {
		t.Run(string(tc.format), func(t *testing.T) {
			b, err := Render(tc.format, g, f, nil)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if len(b) == 0 {
				t.Fatal("render returned no bytes")
			}
			tc.check(t, b)
		})
	}
}

// TestRenderRejectsAnEmptyGrid keeps a zero-size capture from reaching a
// backend that would divide by it.
//
// Negative control: dropping the guard in Render made the nil case panic with
// a nil map dereference. Confirmed on the unfixed tree.
func TestRenderRejectsAnEmptyGrid(t *testing.T) {
	for _, g := range []*Grid{nil, {Cols: 0, Rows: 4}, {Cols: 4, Rows: 0}} {
		if _, err := Render(FormatPNG, g, nil, nil); err == nil {
			t.Errorf("grid %+v rendered instead of erroring", g)
		}
	}
}

// TestPNGGeometryFollowsTheFrame checks that the raster canvas is the size the
// layout says, including padding at scale, so the frame is not silently
// cropped.
//
// Negative control: dropping the `* scale` from layout.pad made the measured
// padding come back 20 px instead of 40 and this fail.
func TestPNGGeometryFollowsTheFrame(t *testing.T) {
	g := NewGrid(10, 4, RGB(0xff, 0xff, 0xff), RGB(0, 0, 0))
	f := testFrame()
	f.Shadow = false
	b, err := RenderPNG(g, f, nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	bounds := img.Bounds()
	// The wash fills the padding, so the very corner must be the wash start
	// and never the card ground.
	corner := img.At(bounds.Min.X, bounds.Min.Y)
	if sameRGB(corner, f.WashStart) == false {
		t.Errorf("top-left corner is %v, want the wash start %v", corner, f.WashStart)
	}
	// Padding is 20 at scale 2, so the card cannot start before x=40.
	pad := f.Padding * f.Scale
	if got := img.At(pad-2, bounds.Dy()/2); sameRGB(got, g.BG) {
		t.Errorf("card ground reaches x=%d, inside the %d px padding", pad-2, pad)
	}
	if got := img.At(pad+8, bounds.Dy()/2); !sameRGB(got, g.BG) {
		t.Errorf("card ground is missing at x=%d, want %v got %v", pad+8, g.BG, got)
	}
}

func sameRGB(c color.Color, want Color) bool {
	r, g, b, _ := c.RGBA()
	return uint8(r>>8) == want.R && uint8(g>>8) == want.G && uint8(b>>8) == want.B
}

// TestTransparentBackgroundKeepsAlpha checks background = none really produces
// a transparent PNG margin instead of a black one.
//
// Negative control: making RenderPNG fill the wash unconditionally turned the
// corner opaque and failed this.
func TestTransparentBackgroundKeepsAlpha(t *testing.T) {
	g := NewGrid(6, 2, RGB(0xff, 0xff, 0xff), RGB(0x10, 0x10, 0x10))
	f := &Frame{Mode: FramePlain, Padding: 12, Radius: 4, Transparent: true, Scale: 1}
	b, err := RenderPNG(g, f, nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
		t.Errorf("corner alpha is %d, want 0 for a transparent background", a>>8)
	}
	mid := image.Pt(img.Bounds().Dx()/2, img.Bounds().Dy()/2)
	if _, _, _, a := img.At(mid.X, mid.Y).RGBA(); a>>8 != 255 {
		t.Errorf("card alpha is %d, want 255", a>>8)
	}
}

// TestBoxDrawingIsGeometryNotText is the fix for the measured font gap: a
// vertical rule must be one continuous column of ink down the whole cell, with
// no seam where a font's glyph would stop short of the line box.
//
// Negative control: deleting the U+2500-257F arm of IsProcedural sent the
// glyph through the font path and left uncovered rows in the column.
func TestBoxDrawingIsGeometryNotText(t *testing.T) {
	g := NewGrid(3, 4, RGB(0xff, 0xff, 0xff), RGB(0, 0, 0))
	for y := range 4 {
		put(g, 1, y, "│", nil)
	}
	b, err := RenderPNG(g, &Frame{Mode: FrameNone, Scale: 2}, nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	bounds := img.Bounds()
	// Every scanline of the image must carry some ink from the rule, because
	// the rule spans all four rows and terminals stretch it edge to edge.
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		inked := false
		for x := bounds.Min.X; x < bounds.Max.X && !inked; x++ {
			r, _, _, _ := img.At(x, y).RGBA()
			inked = r>>8 > 96
		}
		if !inked {
			t.Fatalf("scanline y=%d has no ink: the vertical rule has a seam", y)
		}
	}
	// The SVG backend must draw it as a path, never as a <text> element.
	svg := string(RenderSVG(g, &Frame{Mode: FrameNone}, nil))
	if strings.Contains(svg, "│") {
		t.Error("svg emitted the box glyph as text instead of a path")
	}
	if !strings.Contains(svg, "<path ") {
		t.Error("svg emitted no path for the box glyph")
	}
}

// TestProceduralCoverageIsComplete checks every rune in the covered ranges
// actually produces geometry, so a gap in the switch shows up here rather than
// as a blank cell in someone's screenshot.
//
// Negative control: removing the blockElement dispatch left 0x2580-0x259F with
// no paths and failed 32 runes.
func TestProceduralCoverageIsComplete(t *testing.T) {
	ranges := []struct{ lo, hi rune }{
		{0x2500, 0x257F}, {0x2580, 0x259F}, {0x2800, 0x28FF}, {0xE0B0, 0xE0B7},
	}
	for _, rg := range ranges {
		for r := rg.lo; r <= rg.hi; r++ {
			if !IsProcedural(r) {
				t.Fatalf("U+%04X is in a covered range but IsProcedural says no", r)
			}
			paths, ok := proceduralPaths(r, 12, 24)
			if !ok {
				t.Fatalf("U+%04X returned not-covered", r)
			}
			// U+2800 is the blank braille pattern and U+0020-equivalent blocks
			// are legitimately empty; everything else must draw something.
			if len(paths) == 0 && r != 0x2800 {
				t.Errorf("U+%04X produced no paths", r)
			}
		}
	}
	for _, r := range []rune{'a', ' ', '─' - 1, 0x25A0, 0xE0B8} {
		if IsProcedural(r) {
			t.Errorf("U+%04X is outside the covered ranges but IsProcedural says yes", r)
		}
	}
}

// TestWideClustersHoldTwoCells checks a CJK cluster advances two columns in
// every backend, so a viewer whose font disagrees still keeps the grid.
//
// Negative control: making textRuns treat width-2 cells as ordinary run
// members collapsed the advance to one cell and failed the SVG textLength
// assertion.
func TestWideClustersHoldTwoCells(t *testing.T) {
	g := NewGrid(6, 1, RGB(0xff, 0xff, 0xff), RGB(0, 0, 0))
	g.Cells[0][0] = Cell{Cluster: "日", Width: 2, FG: g.FG, BG: g.BG, BGDefault: true}
	g.Cells[0][1] = Cell{Width: 0, FG: g.FG, BG: g.BG, BGDefault: true}
	put(g, 2, 0, "ab", nil)

	runs := textRuns(g)
	if len(runs) != 2 {
		t.Fatalf("got %d runs, want 2 (the wide cluster split from the text)", len(runs))
	}
	if runs[0].cells != 2 || !runs[0].wide {
		t.Errorf("wide run is cells=%d wide=%v, want 2 and true", runs[0].cells, runs[0].wide)
	}
	if runs[1].col != 2 {
		t.Errorf("the text after a wide cluster starts at col %d, want 2", runs[1].col)
	}

	svg := string(RenderSVG(g, nil, nil))
	// The wide cluster's textLength must be two cells wide.
	want := `textLength="` + fnum(2*svgCellW) + `"`
	if !strings.Contains(svg, want) {
		t.Errorf("svg has no run pinned to two cells (%s)", want)
	}
	if txt := string(RenderText(g)); txt != "日ab\n" {
		t.Errorf("text is %q, want %q", txt, "日ab\n")
	}
}

// TestANSIResetsAtEveryLineEnd keeps a partial paste from bleeding style into
// the host terminal.
//
// Negative control: dropping the trailing reset left the line ending straight
// after the content and failed here.
func TestANSIResetsAtEveryLineEnd(t *testing.T) {
	g := NewGrid(4, 2, RGB(0xff, 0xff, 0xff), RGB(0, 0, 0))
	put(g, 0, 0, "ab", func(c *Cell) { c.BG = RGB(0xff, 0, 0); c.BGDefault = false })
	put(g, 0, 1, "cd", func(c *Cell) { c.Bold = true })
	for i, line := range strings.Split(strings.TrimRight(string(RenderANSI(g)), "\n"), "\n") {
		if !strings.HasSuffix(line, "\x1b[0m") {
			t.Errorf("line %d does not end reset: %q", i, line)
		}
	}
}

// TestBackgroundRunsSkipTheDefault keeps the card ground showing through
// unstyled cells instead of being painted over, which is what lets a
// transparent or washed frame read.
//
// Negative control: making bgRuns emit a run for every cell produced 2 runs
// covering the whole grid and failed the count.
func TestBackgroundRunsSkipTheDefault(t *testing.T) {
	g := NewGrid(8, 1, RGB(0xff, 0xff, 0xff), RGB(0, 0, 0))
	put(g, 2, 0, "xx", func(c *Cell) { c.BG = RGB(0, 0xff, 0); c.BGDefault = false })
	runs := bgRuns(g)
	if len(runs) != 1 {
		t.Fatalf("got %d background runs, want 1", len(runs))
	}
	if runs[0].col != 2 || runs[0].cells != 2 {
		t.Errorf("background run is col=%d cells=%d, want 2 and 2", runs[0].col, runs[0].cells)
	}
}

// TestMakeCellFoldsReverseVideo checks reverse is resolved once, at capture,
// so no backend has to know about it.
//
// Negative control: removing the AttrReverse arm left fg and bg unswapped and
// failed both assertions.
func TestMakeCellFoldsReverseVideo(t *testing.T) {
	p := XTermPalette()
	style := uv.Style{Fg: ansi.BasicColor(1), Bg: ansi.BasicColor(2), Attrs: uv.AttrReverse}
	c := MakeCell("x", 1, style, uv.Link{}, p)
	if c.FG != p.ANSI[2] || c.BG != p.ANSI[1] {
		t.Errorf("reverse did not swap: fg=%v bg=%v", c.FG, c.BG)
	}
	if c.BGDefault {
		t.Error("a reversed cell claims a default background, so its rect would be skipped")
	}
}

// TestPaletteResolvesEveryColorKind is the guard on the one place preserved
// color kinds become concrete RGB.
//
// Negative control: making the IndexedColor arm fall through to `def` turned
// the 256-cube probe into the default colour and failed.
func TestPaletteResolvesEveryColorKind(t *testing.T) {
	p := XTermPalette()
	def := RGB(0x11, 0x22, 0x33)
	if got := p.Resolve(nil, def); got != def {
		t.Errorf("nil resolved to %v, want the default %v", got, def)
	}
	if got := p.Resolve(ansi.BasicColor(4), def); got != xtermBasic[4] {
		t.Errorf("basic 4 resolved to %v, want %v", got, xtermBasic[4])
	}
	// 16 + 36*5 + 6*0 + 0 = 196, the pure red corner of the cube.
	if got := p.Resolve(ansi.IndexedColor(196), def); got != RGB(255, 0, 0) {
		t.Errorf("indexed 196 resolved to %v, want #ff0000", got)
	}
	if got := p.Resolve(ansi.IndexedColor(232), def); got != RGB(8, 8, 8) {
		t.Errorf("indexed 232 resolved to %v, want #080808", got)
	}
	if got := p.Resolve(color.RGBA{R: 1, G: 2, B: 3, A: 255}, def); got != RGB(1, 2, 3) {
		t.Errorf("truecolor resolved to %v, want #010203", got)
	}
	// An Indexed override, the emulator's OSC 4 state, wins over the table.
	p.Indexed = func(i int) (Color, bool) {
		if i == 196 {
			return RGB(9, 9, 9), true
		}
		return Color{}, false
	}
	if got := p.Resolve(ansi.IndexedColor(196), def); got != RGB(9, 9, 9) {
		t.Errorf("the OSC 4 override lost to the table: %v", got)
	}
}

// TestDeriveWashStaysQuiet checks the auto gradient never fights the content:
// both stops must sit under the 3:1 mark floor against the pane background,
// whatever accents the theme hands over.
//
// Negative control: removing the quietStop loop let a saturated accent come
// back at 5.8:1 and failed.
func TestDeriveWashStaysQuiet(t *testing.T) {
	for _, bg := range []Color{RGB(0x1e, 0x1e, 0x2e), RGB(0xff, 0xff, 0xff), RGB(0x28, 0x28, 0x28)} {
		accents := []Color{RGB(0xff, 0x00, 0x00), RGB(0x00, 0xff, 0xff)}
		start, end := DeriveWash(bg, accents)
		for _, stop := range []Color{start, end} {
			if r := contrastRatio(stop, bg); r > 2.25 {
				t.Errorf("wash stop %v is %.2f:1 against %v, loud enough to read as a shape", stop, r, bg)
			}
		}
		if start == end && accents[0] != accents[1] {
			t.Errorf("wash on %v collapsed to a single stop", bg)
		}
	}
}

// TestBuildFrameParsesEveryBackgroundSpelling covers the auto / none / hex /
// hex..hex grammar the config and the CLI both hand over verbatim.
//
// Negative control: dropping the ".." arm sent "112233..445566" to ParseHex,
// which failed, and the gradient silently became the auto wash.
func TestBuildFrameParsesEveryBackgroundSpelling(t *testing.T) {
	in := FrameInputs{Palette: XTermPalette(), Accents: []Color{RGB(0x80, 0x00, 0x80)}}
	base := FrameSpec{Frame: "window", Controls: "auto", Padding: 10, Radius: 4, Scale: 2}

	spec := base
	spec.Background = "none"
	if f := BuildFrame(spec, in); !f.Transparent {
		t.Error(`background "none" did not make the frame transparent`)
	}
	spec.Background = "#112233"
	f := BuildFrame(spec, in)
	if f.WashStart != RGB(0x11, 0x22, 0x33) || f.WashEnd != f.WashStart {
		t.Errorf("a single hex gave %v..%v, want a flat #112233", f.WashStart, f.WashEnd)
	}
	spec.Background = "112233..445566"
	f = BuildFrame(spec, in)
	if f.WashStart != RGB(0x11, 0x22, 0x33) || f.WashEnd != RGB(0x44, 0x55, 0x66) {
		t.Errorf("hex..hex gave %v..%v", f.WashStart, f.WashEnd)
	}
	// Nonsense falls back to the auto wash rather than erroring, because a
	// screenshot with a wrong colour beats no screenshot.
	spec.Background = "not a colour"
	f = BuildFrame(spec, in)
	if f.Transparent || f.WashStart == (Color{}) {
		t.Error("an unparseable background left no wash at all")
	}
}

// TestFrameModesChangeTheGeometry checks none / plain / window really differ,
// so screenshot.frame is not an inert option.
//
// Negative control: making computeLayout ignore FrameWindow's title bar made
// the window and plain heights equal and failed.
func TestFrameModesChangeTheGeometry(t *testing.T) {
	g := NewGrid(10, 5, RGB(0xff, 0xff, 0xff), RGB(0, 0, 0))
	none := computeLayout(g, &Frame{Mode: FrameNone}, 10, 20, 1)
	plain := computeLayout(g, &Frame{Mode: FramePlain, Padding: 16}, 10, 20, 1)
	window := computeLayout(g, &Frame{Mode: FrameWindow, Padding: 16}, 10, 20, 1)
	if none.w != 100 || none.h != 100 {
		t.Errorf("frame none is %vx%v, want the bare 100x100 grid", none.w, none.h)
	}
	if plain.w <= none.w {
		t.Errorf("frame plain (%v) is no wider than frame none (%v)", plain.w, none.w)
	}
	if window.h <= plain.h {
		t.Errorf("frame window (%v) is no taller than frame plain (%v): no title bar", window.h, plain.h)
	}
	if window.titleH == 0 {
		t.Error("frame window has a zero-height title bar")
	}
}

// TestHTMLAndSVGEscapeTheirContent keeps a captured shell line from injecting
// markup into the artifact.
//
// Negative control: swapping xmlEscape for the identity put a live <script>
// into the SVG and failed both cases.
func TestHTMLAndSVGEscapeTheirContent(t *testing.T) {
	g := NewGrid(24, 1, RGB(0xff, 0xff, 0xff), RGB(0, 0, 0))
	put(g, 0, 0, `<script>&"x"`, nil)
	for name, out := range map[string]string{
		"svg":  string(RenderSVG(g, nil, nil)),
		"html": string(RenderHTML(g, nil, nil)),
	} {
		if strings.Contains(out, "<script>") {
			t.Errorf("%s left the markup live", name)
		}
		if !strings.Contains(out, "&lt;script&gt;") || !strings.Contains(out, "&amp;") {
			t.Errorf("%s did not escape the content", name)
		}
	}
}

// TestLinksSurviveAsAnchors checks OSC 8 targets reach the two formats that
// can carry them.
//
// Negative control: dropping the Link arm of writeSVGRun removed the <a> and
// failed the svg case.
func TestLinksSurviveAsAnchors(t *testing.T) {
	g := NewGrid(8, 1, RGB(0xff, 0xff, 0xff), RGB(0, 0, 0))
	put(g, 0, 0, "click", func(c *Cell) { c.Link = "https://example.com/a?b=1&c=2" })
	for name, out := range map[string]string{
		"svg":  string(RenderSVG(g, nil, nil)),
		"html": string(RenderHTML(g, nil, nil)),
	} {
		if !strings.Contains(out, `<a href="https://example.com/a?b=1&amp;c=2">`) {
			t.Errorf("%s carries no escaped anchor:\n%s", name, out)
		}
	}
}

// TestScaleChangesThePNGSize keeps screenshot.scale from being inert.
//
// Negative control: pinning scale to 2 inside RenderPNG made every size equal
// and failed.
func TestScaleChangesThePNGSize(t *testing.T) {
	g := NewGrid(8, 2, RGB(0xff, 0xff, 0xff), RGB(0, 0, 0))
	var prev int
	for scale := 1; scale <= 4; scale++ {
		b, err := RenderPNG(g, &Frame{Mode: FramePlain, Padding: 8, Scale: scale}, nil)
		if err != nil {
			t.Fatalf("scale %d: %v", scale, err)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(b))
		if err != nil {
			t.Fatalf("scale %d decode: %v", scale, err)
		}
		if cfg.Width <= prev {
			t.Errorf("scale %d is %d px wide, no wider than scale %d", scale, cfg.Width, scale-1)
		}
		prev = cfg.Width
	}
}

// TestParseFormatAndExtensions pins the CLI's spellings.
//
// Negative control: making Ext return string(f) for every format gave ANSI the
// ".ansi" extension and failed.
func TestParseFormatAndExtensions(t *testing.T) {
	for in, want := range map[string]Format{
		"png": FormatPNG, "svg": FormatSVG, "ansi": FormatANSI,
		"ans": FormatANSI, "html": FormatHTML, "htm": FormatHTML,
		"txt": FormatText, "text": FormatText,
	} {
		got, ok := ParseFormat(in)
		if !ok || got != want {
			t.Errorf("ParseFormat(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	if _, ok := ParseFormat("webp"); ok {
		t.Error("ParseFormat accepted a format with no backend")
	}
	if FormatANSI.Ext() != "ans" {
		t.Errorf("ansi extension is %q, want ans", FormatANSI.Ext())
	}
	if FormatPNG.MediaType() != "image/png" {
		t.Errorf("png media type is %q", FormatPNG.MediaType())
	}
}

// TestRenderIsDeterministic checks two renders of one grid are byte-identical,
// which a golden file or a diffable ANSI export both depend on.
//
// This control passes both ways deliberately: nothing in the package is
// randomised today, and the test exists to catch a map iteration or a time
// stamp being introduced later.
func TestRenderIsDeterministic(t *testing.T) {
	g, f := testGrid(), testFrame()
	for _, format := range []Format{FormatPNG, FormatSVG, FormatANSI, FormatHTML, FormatText} {
		a, err := Render(format, g, f, nil)
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		b, err := Render(format, g, f, nil)
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		if !bytes.Equal(a, b) {
			t.Errorf("%s renders differently on a second call", format)
		}
	}
}

// TestANSIDoesNotResetBetweenUnstyledCells is the guard on the run merger in
// the ANSI backend: unstyled text must come out as plain text, not as every
// word wrapped in its own reset.
//
// Negative control: the merger it replaced compared whole cells and treated
// "no style" as a style, so a line of prose came back as
// "\x1b[0mfatal:\x1b[0m \x1b[0mnot\x1b[0m ..." — this fails on that tree with
// 22 escapes for 3 words.
func TestANSIDoesNotResetBetweenUnstyledCells(t *testing.T) {
	g := NewGrid(24, 1, RGB(0xff, 0xff, 0xff), RGB(0, 0, 0))
	put(g, 0, 0, "not a git repository", nil)
	got := string(RenderANSI(g))
	if want := "not a git repository\x1b[0m\n"; got != want {
		t.Errorf("unstyled prose came out as %q, want %q", got, want)
	}
}

// TestANSIEmitsOneEscapePerStyleChange keeps the merged runs from re-emitting
// the same SGR cell by cell.
//
// Negative control: same tree as above; the styled word alone produced six
// escape sequences instead of two.
func TestANSIEmitsOneEscapePerStyleChange(t *testing.T) {
	g := NewGrid(16, 1, RGB(0xff, 0xff, 0xff), RGB(0, 0, 0))
	put(g, 0, 0, "ok", nil)
	put(g, 3, 0, "green", func(c *Cell) { c.FG = RGB(0, 0xff, 0) })
	put(g, 9, 0, "plain", nil)
	got := string(RenderANSI(g))
	if n := strings.Count(got, "\x1b["); n != 3 {
		t.Errorf("got %d escapes in %q, want 3: set, reset-to-plain, line-end reset", n, got)
	}
	if !strings.Contains(got, "\x1b[38;2;0;255;0mgreen\x1b[0m") {
		t.Errorf("the styled run is not one span: %q", got)
	}
}
