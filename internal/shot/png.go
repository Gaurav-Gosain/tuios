package shot

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// PNG backend: direct cell rasterization, not SVG-to-PNG. freeze's route
// (resvg compiled to WASM) works but drags a WASM runtime into the binary;
// the direct blitter shares the procedural glyph code the SVG backend needs
// anyway and renders in milliseconds. Embedded Go Mono regular and bold are
// the fallback faces; Frame.FontData loads the user's own font so their
// Nerd Font icons render, and a glyph in neither font draws as a dotted
// outline box: visible tofu rather than silent blanks.

const pngBaseFontSize = 14.0

// RenderPNG renders the grid, frame, and decorations to PNG bytes.
func RenderPNG(g *Grid, f *Frame, decorations []Decoration) ([]byte, error) {
	img, err := Raster(g, f, decorations...)
	if err != nil {
		return nil, err
	}
	return EncodePNG(img)
}

// Raster draws the capture and stops there, handing back the pixels.
//
// It is split out of RenderPNG because the preview and the file want the same
// pixels at different sizes: the file wants them encoded whole, the panel wants
// them shrunk to the box it will draw them in. Rendering twice cost a full
// second on a full-screen capture and produced two pictures that had to be kept
// in step; rendering once and encoding twice cannot drift.
func Raster(g *Grid, f *Frame, decorations ...Decoration) (*image.RGBA, error) {
	_ = decorations
	scale := 2
	if f != nil && f.Scale >= 1 && f.Scale <= 4 {
		scale = f.Scale
	}
	size := pngBaseFontSize * float64(scale)

	faces, err := loadFaces(f, size)
	if err != nil {
		return nil, err
	}
	cw, ch := faces.cellSize()
	cw, ch = fitCellToTerminal(f, cw, ch)
	l := computeLayout(g, f, cw, ch, float64(scale))

	img := image.NewRGBA(image.Rect(0, 0, int(l.w+0.5), int(l.h+0.5)))
	framed := f != nil && f.Mode != FrameNone
	if framed {
		if !f.Transparent {
			fillDiagonalGradient(img, f.WashStart, f.WashEnd)
		}
		if f.Shadow {
			drawCardShadow(img, l, ch)
		}
		fillRoundedRect(img, l.cardX, l.cardY, l.cardW, l.cardH, l.radius, g.BG)
		if f.Mode == FrameWindow {
			drawPNGTitleBar(img, g, f, l, faces)
		}
	} else {
		draw.Draw(img, img.Bounds(), image.NewUniform(g.BG), image.Point{}, draw.Src)
	}

	for _, r := range bgRuns(g) {
		fillRect(img, l.gridX+float64(r.col)*cw, l.gridY+float64(r.row)*ch,
			float64(r.cells)*cw, ch, r.color)
	}
	for _, r := range textRuns(g) {
		drawPNGRun(img, r, l, faces, g)
	}
	return img, nil
}

// EncodePNG serialises a raster. BestSpeed because the picture is a screenshot
// on its way to a file or a terminal, and the seconds a smaller file would save
// are not seconds anybody spends.
func EncodePNG(img *image.RGBA) ([]byte, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}
	return buf.Bytes(), nil
}

// Downscale shrinks img to fit maxW by maxH, keeping its proportions. An image
// already inside the box comes back untouched.
//
// This is a box filter over integer source rectangles: the preview is a
// picture of text being looked at from across a panel, and the difference
// between this and a Lanczos kernel there is invisible, while the difference in
// what it costs is the whole reason the preview arrives at all.
func Downscale(img *image.RGBA, maxW, maxH int) *image.RGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 || maxW <= 0 || maxH <= 0 || (w <= maxW && h <= maxH) {
		return img
	}
	outW, outH := w, h
	if outW > maxW {
		outW, outH = maxW, max(1, h*maxW/w)
	}
	if outH > maxH {
		outH, outW = maxH, max(1, outW*maxH/outH)
	}
	out := image.NewRGBA(image.Rect(0, 0, outW, outH))
	for oy := range outH {
		y0, y1 := b.Min.Y+oy*h/outH, b.Min.Y+(oy+1)*h/outH
		if y1 <= y0 {
			y1 = y0 + 1
		}
		row := out.Pix[oy*out.Stride:]
		for ox := range outW {
			x0, x1 := b.Min.X+ox*w/outW, b.Min.X+(ox+1)*w/outW
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var sr, sg, sb, sa, n uint32
			for y := y0; y < y1; y++ {
				p := img.Pix[(y-b.Min.Y)*img.Stride+(x0-b.Min.X)*4:]
				for x := 0; x < x1-x0; x++ {
					sr += uint32(p[x*4])
					sg += uint32(p[x*4+1])
					sb += uint32(p[x*4+2])
					sa += uint32(p[x*4+3])
					n++
				}
			}
			row[ox*4] = uint8(sr / n)
			row[ox*4+1] = uint8(sg / n)
			row[ox*4+2] = uint8(sb / n)
			row[ox*4+3] = uint8(sa / n)
		}
	}
	return out
}

// faceSet is the resolved fallback order: the user's font first when there is
// one, then Go Mono, each with its bold cut when one was found.
type faceSet struct {
	user, userBold, regular, bold                 font.Face
	userFont, userBoldFont, regularFont, boldFont *sfnt.Font
	size                                          float64
	buf                                           sfnt.Buffer
}

func loadFaces(f *Frame, size float64) (*faceSet, error) {
	fs := &faceSet{size: size}
	opts := &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull}
	var err error
	if fs.regularFont, err = opentype.Parse(gomono.TTF); err != nil {
		return nil, fmt.Errorf("parse embedded font: %w", err)
	}
	if fs.regular, err = opentype.NewFace(fs.regularFont, opts); err != nil {
		return nil, fmt.Errorf("embedded face: %w", err)
	}
	if fs.boldFont, err = opentype.Parse(gomonobold.TTF); err != nil {
		return nil, fmt.Errorf("parse embedded bold font: %w", err)
	}
	if fs.bold, err = opentype.NewFace(fs.boldFont, opts); err != nil {
		return nil, fmt.Errorf("embedded bold face: %w", err)
	}
	if f != nil && len(f.FontData) > 0 {
		uf, err := opentype.Parse(f.FontData)
		if err != nil {
			return nil, fmt.Errorf("screenshot.font_file did not parse: %w", err)
		}
		face, err := opentype.NewFace(uf, opts)
		if err != nil {
			return nil, fmt.Errorf("screenshot.font_file face: %w", err)
		}
		fs.userFont, fs.user = uf, face
		// The bold cut is optional and its failure is not the capture's. A font
		// that will not parse as bold leaves the double-strike in place, which
		// is what happened before there was a bold cut at all.
		if len(f.BoldFontData) > 0 {
			if bf, berr := opentype.Parse(f.BoldFontData); berr == nil {
				if bface, berr := opentype.NewFace(bf, opts); berr == nil {
					fs.userBoldFont, fs.userBold = bf, bface
				}
			}
		}
	}
	return fs, nil
}

// cellSize derives the grid cell from the primary face: the advance of "M"
// wide, the face's own line box tall.
//
// The height used to be a hardcoded 1.25 em while the width came from the
// font, so the cell's shape was half measured and half guessed. Against
// JetBrainsMono, whose advance is 0.600 em and whose hhea line box is 1.320 em,
// that gave 0.486 where the terminal itself draws 0.455: every saved capture
// came out about seven percent wider per cell than the screen it pictured,
// uniformly, which is what "horizontally stretched" looks like to someone
// holding the picture up next to their own terminal. Metrics().Height is
// ascent - descent + lineGap, which is the same line box kitty and ghostty
// size their cells from, so taking it makes the picture's aspect the host's.
func (fs *faceSet) cellSize() (float64, float64) {
	face := fs.regular
	if fs.user != nil {
		face = fs.user
	}
	adv, ok := face.GlyphAdvance('M')
	if !ok || adv == 0 {
		adv = fixed.I(int(fs.size * 0.6))
	}
	cw := fixedToFloat(adv)
	ch := fixedToFloat(face.Metrics().Height)
	if ch <= 0 {
		// A face with no usable metrics still has to draw somewhere. The old
		// hardcode is the fallback rather than the rule.
		ch = fs.size * 1.25
	}
	return cw, ch
}

// fitCellToTerminal gives the raster's cell the shape of the cell of the
// terminal being pictured.
//
// The font's own cell is not the terminal's. cellSize takes the width from the
// 'M' advance and the height from the face's line box, and a terminal picks its
// own height from ascent and descent and its own leading rules; the two agree
// only by luck. Measured on this machine against a real kitty: the host's cell
// was 9 by 20 pixels, a ratio of 0.450, while the raster's cell in the built-in
// face was 5.76 by 14.4, a ratio of 0.400. Every column of the picture was
// eleven percent narrower than the column it pictured, which is a whole screen
// leaning tall, and no amount of correct letterboxing in the panel can undo it
// because the picture itself is the wrong shape.
//
// The cell is only ever grown, never shrunk. A cell narrower than the advance
// would run neighbouring glyphs into each other and a cell shorter than the
// line box would clip them; a cell wider than the advance costs a glyph that is
// centred in it, which drawCluster already does.
func fitCellToTerminal(f *Frame, cw, ch float64) (float64, float64) {
	if f == nil || f.CellAspect <= 0 || cw <= 0 || ch <= 0 {
		return cw, ch
	}
	if want := ch * f.CellAspect; want > cw {
		return want, ch
	}
	return cw, cw / f.CellAspect
}

// pick returns the face and font that can draw r, honoring bold, or nil
// when nothing covers it (tofu).
func (fs *faceSet) pick(r rune, bold bool) (font.Face, bool) {
	if bold && fs.userBoldFont != nil {
		if idx, err := fs.userBoldFont.GlyphIndex(&fs.buf, r); err == nil && idx != 0 {
			return fs.userBold, false
		}
	}
	if fs.userFont != nil {
		if idx, err := fs.userFont.GlyphIndex(&fs.buf, r); err == nil && idx != 0 {
			// No bold cut for this glyph, so the caller thickens it by hand.
			return fs.user, bold
		}
	}
	if bold {
		if idx, err := fs.boldFont.GlyphIndex(&fs.buf, r); err == nil && idx != 0 {
			return fs.bold, false
		}
	}
	if idx, err := fs.regularFont.GlyphIndex(&fs.buf, r); err == nil && idx != 0 {
		return fs.regular, false
	}
	return nil, false
}

func fixedToFloat(v fixed.Int26_6) float64 { return float64(v) / 64 }

func drawPNGRun(img *image.RGBA, r run, l layout, faces *faceSet, g *Grid) {
	x := l.gridX + float64(r.col)*l.cw
	y := l.gridY + float64(r.row)*l.ch
	w := float64(r.cells) * l.cw
	c := r.cell
	ink := c.FG
	if c.Faint {
		ink = Mix(c.FG, c.BG, 0.45)
	}

	if r.procedural != 0 {
		paths, _ := proceduralPaths(r.procedural, l.cw, l.ch)
		for _, p := range paths {
			pc := ink
			if p.opacity > 0 {
				pc = Mix(c.BG, ink, p.opacity)
			}
			fillPath(img, p, x, y, pc)
		}
		drawPNGDecor(img, r, x, y, w, l, ink)
		return
	}

	if !r.isBlank() {
		baseline := y + l.ch*0.5 + faces.size*0.32
		cx := x
		for _, cl := range runClusters(r) {
			cellW := l.cw
			if r.wide {
				cellW = 2 * l.cw
			}
			drawCluster(img, faces, cl, cx, baseline, cellW, l.ch, y, ink, c.Bold, c.Italic)
			cx += cellW
		}
	}
	drawPNGDecor(img, r, x, y, w, l, ink)
}

// clusters splits run text into per-cell clusters. Merged narrow runs carry
// one single-rune cluster per cell by construction (multi-rune clusters get
// their own run in textRuns), so splitting by rune is exact.
func clusters(s string) []string {
	var out []string
	for _, r := range s {
		out = append(out, string(r))
	}
	return out
}

// runClusters returns the per-cell clusters of a run.
func runClusters(r run) []string {
	if r.wide || r.cells == 1 {
		return []string{r.text}
	}
	return clusters(r.text)
}

func drawCluster(img *image.RGBA, faces *faceSet, cl string, x, baseline, cellW, cellH, cellTop float64, ink Color, bold, italic bool) {
	if cl == " " || cl == "" {
		return
	}
	r := firstRune(cl)
	face, doubleStrike := faces.pick(r, bold)
	if face == nil {
		drawTofu(img, x, cellTop, cellW, cellH, ink)
		return
	}
	d := font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(ink),
		Face: face,
		Dot:  fixed.Point26_6{X: floatToFixed(x), Y: floatToFixed(baseline)},
	}
	// Center the glyph in its cell when the face advance disagrees with
	// the grid, so a substituted glyph does not lean left.
	if adv, ok := face.GlyphAdvance(r); ok {
		off := (cellW - fixedToFloat(adv)) / 2
		if off > 0.5 {
			d.Dot.X = floatToFixed(x + off)
		}
	}
	d.DrawString(cl)
	if bold && doubleStrike {
		d.Dot = fixed.Point26_6{X: floatToFixed(x + 1), Y: floatToFixed(baseline)}
		d.DrawString(cl)
	}
	_ = italic // no italic face embedded; the honest limitation is documented
}

// drawTofu draws the deliberate missing-glyph mark: a dotted outline box.
func drawTofu(img *image.RGBA, x, y, w, h float64, ink Color) {
	inset := w * 0.15
	x0, y0 := x+inset, y+h*0.18
	x1, y1 := x+w-inset, y+h*0.82
	dot := max(1.0, w/8)
	step := dot * 2.2
	for cx := x0; cx <= x1; cx += step {
		fillRect(img, cx, y0, dot, dot, ink)
		fillRect(img, cx, y1-dot, dot, dot, ink)
	}
	for cy := y0; cy <= y1; cy += step {
		fillRect(img, x0, cy, dot, dot, ink)
		fillRect(img, x1-dot, cy, dot, dot, ink)
	}
}

func drawPNGDecor(img *image.RGBA, r run, x, y, w float64, l layout, ink Color) {
	c := r.cell
	t := max(1.0, l.ch/14)
	uy := y + l.ch - t*2
	switch c.Underline {
	case UnderlineSingle:
		fillRect(img, x, uy, w, t, ink)
	case UnderlineDouble:
		fillRect(img, x, uy-t*1.8, w, t, ink)
		fillRect(img, x, uy, w, t, ink)
	case UnderlineCurly:
		amp := t * 1.2
		for px := 0.0; px < w; px++ {
			phase := px / l.cw * 2 * math.Pi
			dy := amp * math.Sin(phase)
			fillRect(img, x+px, uy+dy, 1, t, ink)
		}
	case UnderlineDotted:
		for px := 0.0; px < w; px += t * 2.5 {
			fillRect(img, x+px, uy, t, t, ink)
		}
	case UnderlineDashed:
		for px := 0.0; px < w; px += t * 5 {
			fillRect(img, x+px, uy, t*3, t, ink)
		}
	}
	if c.Strike {
		fillRect(img, x, y+l.ch*0.5-t/2, w, t, ink)
	}
}

func drawPNGTitleBar(img *image.RGBA, g *Grid, f *Frame, l layout, faces *faceSet) {
	rule := Mix(g.BG, g.FG, 0.12)
	fillRect(img, l.cardX, l.cardY+l.titleH-1, l.cardW, 1, rule)
	cy := l.cardY + l.titleH/2
	r := l.titleH * 0.17
	gap := r * 3.2
	cx := l.cardX + l.inset + r
	drawDot := func(i int, c Color) {
		var p gpath
		b := &glyphBuilder{w: r * 2, h: r * 2}
		b.dot(r, r, r)
		p = b.paths[0]
		fillPath(img, p, cx+float64(i)*gap-r, cy-r, c)
	}
	switch f.Controls {
	case ControlsMacOS:
		drawDot(0, RGB(0xff, 0x5f, 0x57))
		drawDot(1, RGB(0xfe, 0xbc, 0x2e))
		drawDot(2, RGB(0x28, 0xc8, 0x40))
	case ControlsGlyphs:
		baseline := cy + faces.size*0.34
		for i, m := range []string{f.CloseGlyph, f.MinimizeGlyph, f.MaximizeGlyph} {
			if m == "" {
				continue
			}
			drawCluster(img, faces, m, cx+float64(i)*gap-l.cw/2, baseline, l.cw, l.ch, cy-l.ch/2, f.Accent, false, false)
		}
	case ControlsNone:
	}
	if f.Title != "" {
		ink := Mix(g.FG, g.BG, 0.3)
		tw := float64(len([]rune(f.Title))) * l.cw
		tx := l.cardX + (l.cardW-tw)/2
		baseline := cy + faces.size*0.34
		cx := tx
		for _, cl := range clusters(f.Title) {
			drawCluster(img, faces, cl, cx, baseline, l.cw, l.ch, cy-l.ch/2, ink, false, false)
			cx += l.cw
		}
	}
}

// fillRect fills an axis-aligned rect with an opaque color.
func fillRect(img *image.RGBA, x, y, w, h float64, c Color) {
	r := image.Rect(int(x+0.5), int(y+0.5), int(x+w+0.5), int(y+h+0.5))
	draw.Draw(img, r, image.NewUniform(c), image.Point{}, draw.Over)
}

// fillPath rasterizes one gpath at an origin with anti-aliasing.
func fillPath(img *image.RGBA, p gpath, ox, oy float64, c Color) {
	minX, minY, maxX, maxY := pathBounds(p, ox, oy)
	// Clip to the canvas before sizing the rasterizer. A procedural glyph in
	// the first or last column deliberately bleeds edgeBleed past its cell to
	// close the seam with its neighbour, and at frame none that cell edge is
	// the canvas edge, so an unclipped rect indexes past the pixel buffer.
	// The rasterizer accepts path coordinates outside its own bounds, so
	// clipping the rect is all that is needed and the geometry is unchanged.
	clip := img.Bounds()
	x0, y0 := max(int(minX), clip.Min.X), max(int(minY), clip.Min.Y)
	x1, y1 := min(int(maxX+1), clip.Max.X), min(int(maxY+1), clip.Max.Y)
	if x1 <= x0 || y1 <= y0 {
		return
	}
	ras := vector.NewRasterizer(x1-x0, y1-y0)
	ras.DrawOp = draw.Over
	fx, fy := float32(ox)-float32(x0), float32(oy)-float32(y0)
	for _, o := range p.ops {
		switch o.op {
		case 'M':
			ras.MoveTo(fx+float32(o.x), fy+float32(o.y))
		case 'L':
			ras.LineTo(fx+float32(o.x), fy+float32(o.y))
		case 'Q':
			ras.QuadTo(fx+float32(o.c1x), fy+float32(o.c1y), fx+float32(o.x), fy+float32(o.y))
		case 'C':
			ras.CubeTo(fx+float32(o.c1x), fy+float32(o.c1y), fx+float32(o.c2x), fy+float32(o.c2y), fx+float32(o.x), fy+float32(o.y))
		case 'Z':
			ras.ClosePath()
		}
	}
	ras.Draw(img, image.Rect(x0, y0, x1, y1), image.NewUniform(c), image.Point{})
}

func pathBounds(p gpath, ox, oy float64) (minX, minY, maxX, maxY float64) {
	first := true
	add := func(x, y float64) {
		if first {
			minX, minY, maxX, maxY = x, y, x, y
			first = false
			return
		}
		minX, minY = min(minX, x), min(minY, y)
		maxX, maxY = max(maxX, x), max(maxY, y)
	}
	for _, o := range p.ops {
		if o.op == 'Z' {
			continue
		}
		add(ox+o.x, oy+o.y)
		if o.op == 'Q' || o.op == 'C' {
			add(ox+o.c1x, oy+o.c1y)
		}
		if o.op == 'C' {
			add(ox+o.c2x, oy+o.c2y)
		}
	}
	return
}

// fillDiagonalGradient paints the wash: a top-left to bottom-right blend.
//
// The blend runs on x+y, so it is constant along every anti-diagonal. Mixing
// per pixel therefore computed the same colour w times per diagonal and cost
// 58 ms on a full-screen canvas; a lookup table indexed by x+y computes it
// w+h times and copies the rest, which measured 17 ms for a picture nobody can
// tell apart. Writing straight into Pix rather than through SetRGBA drops the
// per-pixel bounds check the loop already made unnecessary.
func fillDiagonalGradient(img *image.RGBA, start, end Color) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	span := float64(w + h)
	if w <= 0 || h <= 0 || span == 0 {
		return
	}
	lut := make([]Color, w+h)
	for d := range lut {
		lut[d] = Mix(start, end, float64(d)/span)
	}
	for y := range h {
		row := img.Pix[y*img.Stride : y*img.Stride+w*4]
		for x := range w {
			c := lut[x+y]
			row[x*4], row[x*4+1], row[x*4+2], row[x*4+3] = c.R, c.G, c.B, c.A
		}
	}
}

// fillRoundedRect fills a rounded rectangle with anti-aliased corners.
func fillRoundedRect(img *image.RGBA, x, y, w, h, radius float64, c Color) {
	fillPath(img, roundedRectPath(0, 0, w, h, radius), x, y, c)
}

func roundedRectPath(x, y, w, h, r float64) gpath {
	if r <= 0 {
		return gpath{ops: []gop{
			{op: 'M', x: x, y: y}, {op: 'L', x: x + w, y: y},
			{op: 'L', x: x + w, y: y + h}, {op: 'L', x: x, y: y + h}, {op: 'Z'},
		}}
	}
	r = min(r, min(w, h)/2)
	k := r * kappa
	return gpath{ops: []gop{
		{op: 'M', x: x + r, y: y},
		{op: 'L', x: x + w - r, y: y},
		{op: 'C', c1x: x + w - r + k, c1y: y, c2x: x + w, c2y: y + r - k, x: x + w, y: y + r},
		{op: 'L', x: x + w, y: y + h - r},
		{op: 'C', c1x: x + w, c1y: y + h - r + k, c2x: x + w - r + k, c2y: y + h, x: x + w - r, y: y + h},
		{op: 'L', x: x + r, y: y + h},
		{op: 'C', c1x: x + r - k, c1y: y + h, c2x: x, c2y: y + h - r + k, x: x, y: y + h - r},
		{op: 'L', x: x, y: y + r},
		{op: 'C', c1x: x, c1y: y + r - k, c2x: x + r - k, c2y: y, x: x + r, y: y},
		{op: 'Z'},
	}}
}

// shadowScale is how much smaller than the canvas the shadow is worked out at.
//
// A double box blur has no detail in it: it is a featureless ramp a few dozen
// pixels wide, and blurring it at full resolution spent 388 ms of a 903 ms
// full-screen render on pixels that carry no information. Rasterising the
// silhouette and blurring it at a quarter on each axis is sixteen times less
// work and measured 63 ms, and the result is interpolated back up rather than
// stepped up, so no seam appears where the sample grid does.
const shadowScale = 4

// drawCardShadow blurs the card silhouette and composites it under where
// the card will land, offset down: carbon's single soft shadow.
func drawCardShadow(img *image.RGBA, l layout, ch float64) {
	radius := int(ch * 0.5)
	dy := ch * 0.45
	// Render the silhouette into an alpha mask with a margin for the blur.
	margin := radius * 3
	mw := int(l.cardW) + 2*margin
	mh := int(l.cardH) + 2*margin
	if mw <= 0 || mh <= 0 {
		return
	}

	q := float64(shadowScale)
	sw, sh := (mw+shadowScale-1)/shadowScale, (mh+shadowScale-1)/shadowScale
	small := image.NewAlpha(image.Rect(0, 0, sw, sh))
	silhouette := image.NewRGBA(small.Bounds())
	fillPath(silhouette, roundedRectPath(0, 0, l.cardW/q, l.cardH/q, l.radius/q),
		float64(margin)/q, float64(margin)/q, RGB(0, 0, 0))
	for i := 3; i < len(silhouette.Pix); i += 4 {
		small.Pix[i/4] = silhouette.Pix[i]
	}
	blur := max(1, radius/shadowScale)
	boxBlurAlpha(small, blur)
	boxBlurAlpha(small, blur)
	mask := upscaleAlpha(small, mw, mh)

	// Composite at 35 percent black.
	ox := int(l.cardX) - margin
	oy := int(l.cardY+dy) - margin
	draw.DrawMask(img, image.Rect(ox, oy, ox+mw, oy+mh),
		image.NewUniform(color.RGBA{A: 89}), image.Point{}, mask, image.Point{}, draw.Over)
}

// upscaleAlpha resamples an alpha mask up to w by h, linearly on both axes.
//
// The x weights are worked out once per row width rather than once per pixel,
// and everything is integer: the mask is nine megapixels on a full-screen
// capture, so a float multiply per sample would give back most of what the
// smaller blur just saved.
func upscaleAlpha(src *image.Alpha, w, h int) *image.Alpha {
	dst := image.NewAlpha(image.Rect(0, 0, w, h))
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	if sw <= 0 || sh <= 0 || w <= 0 || h <= 0 {
		return dst
	}
	// Fixed point with 8 fractional bits, and half-pixel centres so the
	// resampled mask sits where the small one did rather than a sample to its
	// left.
	type tap struct {
		lo, hi int
		frac   int32
	}
	taps := func(n, out, limit int) []tap {
		t := make([]tap, out)
		for i := range out {
			// int64 because the product is a pixel count times a pixel count
			// times 256, which leaves 32 bits behind on a large canvas.
			f := int(((int64(2*i+1)*int64(n)*256)/int64(2*out) - 128))
			f = max(f, 0)
			lo := f >> 8
			if lo >= limit-1 {
				t[i] = tap{lo: limit - 1, hi: limit - 1}
				continue
			}
			t[i] = tap{lo: lo, hi: lo + 1, frac: int32(f & 0xff)}
		}
		return t
	}
	xt := taps(sw, w, sw)
	yt := taps(sh, h, sh)
	for y := range h {
		ty := yt[y]
		r0 := src.Pix[ty.lo*src.Stride:]
		r1 := src.Pix[ty.hi*src.Stride:]
		out := dst.Pix[y*dst.Stride : y*dst.Stride+w]
		for x := range w {
			t := xt[x]
			a := int32(r0[t.lo])<<8 + (int32(r0[t.hi])-int32(r0[t.lo]))*t.frac
			b := int32(r1[t.lo])<<8 + (int32(r1[t.hi])-int32(r1[t.lo]))*t.frac
			out[x] = uint8((a + (b-a)*ty.frac/256) >> 8)
		}
	}
	return dst
}

// boxBlurAlpha runs one separable box blur pass over an alpha mask.
func boxBlurAlpha(m *image.Alpha, radius int) {
	if radius < 1 {
		return
	}
	b := m.Bounds()
	w, h := b.Dx(), b.Dy()
	tmp := make([]uint8, w*h)
	window := 2*radius + 1
	// Horizontal.
	for y := 0; y < h; y++ {
		row := m.Pix[y*m.Stride : y*m.Stride+w]
		sum := 0
		for x := -radius; x <= radius; x++ {
			sum += int(row[clampInt(x, w)])
		}
		for x := 0; x < w; x++ {
			tmp[y*w+x] = uint8(sum / window)
			sum += int(row[clampInt(x+radius+1, w)]) - int(row[clampInt(x-radius, w)])
		}
	}
	// Vertical.
	for x := 0; x < w; x++ {
		sum := 0
		for y := -radius; y <= radius; y++ {
			sum += int(tmp[clampInt(y, h)*w+x])
		}
		for y := 0; y < h; y++ {
			m.Pix[y*m.Stride+x] = uint8(sum / window)
			sum += int(tmp[clampInt(y+radius+1, h)*w+x]) - int(tmp[clampInt(y-radius, h)*w+x])
		}
	}
}

func clampInt(i, n int) int {
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

func floatToFixed(f float64) fixed.Int26_6 { return fixed.Int26_6(f * 64) }
