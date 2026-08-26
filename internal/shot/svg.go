package shot

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// SVG backend. Real <text> and <rect> elements, never foreignObject: carbon
// and ray.so export HTML wrapped in foreignObject and design tools render it
// blank. We have a cell grid, so we do not need their trap. Every run gets
// an explicit x from the grid plus textLength with spacingAndGlyphs, so a
// viewer with a different font still keeps the columns; background rects use
// exact grid widths, so freeze's half-cell fudge and its gap bug are
// unconstructible here.

const (
	svgFontSize = 14.0
	// svgCellW is the advance of a monospace cell at svgFontSize. 0.6em is
	// JetBrains Mono's advance; textLength pins other fonts to the grid
	// anyway, this only sets the grid's absolute scale.
	svgCellW = svgFontSize * 0.6
	svgCellH = svgFontSize * 1.3
)

// RenderSVG renders the grid, frame, and decorations to an SVG document.
func RenderSVG(g *Grid, f *Frame, decorations []Decoration) []byte {
	l := computeLayout(g, f, svgCellW, svgCellH, 1)
	var b strings.Builder
	b.Grow(1 << 15)
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%s" height="%s" viewBox="0 0 %s %s">`+"\n",
		fnum(l.w), fnum(l.h), fnum(l.w), fnum(l.h))

	family := "monospace"
	if f != nil && f.FontFamily != "" {
		family = f.FontFamily
	}
	writeSVGDefs(&b, g, f, l, family)

	framed := f != nil && f.Mode != FrameNone
	if framed {
		if !f.Transparent {
			fmt.Fprintf(&b, `<rect width="%s" height="%s" fill="url(#wash)"/>`+"\n", fnum(l.w), fnum(l.h))
		}
		shadow := ""
		if f.Shadow {
			shadow = ` filter="url(#cardshadow)"`
		}
		fmt.Fprintf(&b, `<rect x="%s" y="%s" width="%s" height="%s" rx="%s" fill="%s"%s/>`+"\n",
			fnum(l.cardX), fnum(l.cardY), fnum(l.cardW), fnum(l.cardH), fnum(l.radius), Hex(g.BG), shadow)
		if f.Mode == FrameWindow {
			writeSVGTitleBar(&b, g, f, l, family)
		}
	} else {
		fmt.Fprintf(&b, `<rect width="%s" height="%s" fill="%s"/>`+"\n", fnum(l.w), fnum(l.h), Hex(g.BG))
	}

	// The content group carries the one transform from cell units to
	// pixels; a decoration layer appended later addresses (col,row) and
	// inherits this geometry.
	fmt.Fprintf(&b, `<g transform="translate(%s,%s)">`+"\n", fnum(l.gridX), fnum(l.gridY))
	for _, r := range bgRuns(g) {
		fmt.Fprintf(&b, `<rect x="%s" y="%s" width="%s" height="%s" fill="%s"/>`+"\n",
			fnum(float64(r.col)*l.cw), fnum(float64(r.row)*l.ch),
			fnum(float64(r.cells)*l.cw), fnum(l.ch), Hex(r.color))
	}
	for _, r := range textRuns(g) {
		writeSVGRun(&b, r, l)
	}
	_ = decorations // reserved: annotation shapes land in this group
	b.WriteString("</g>\n</svg>\n")
	return []byte(b.String())
}

func writeSVGDefs(b *strings.Builder, g *Grid, f *Frame, l layout, family string) {
	b.WriteString("<defs>\n")
	if f != nil && f.Mode != FrameNone {
		if !f.Transparent {
			fmt.Fprintf(b, `<linearGradient id="wash" x1="0" y1="0" x2="1" y2="1">`+
				`<stop offset="0" stop-color="%s"/><stop offset="1" stop-color="%s"/></linearGradient>`+"\n",
				Hex(f.WashStart), Hex(f.WashEnd))
		}
		if f.Shadow {
			fmt.Fprintf(b, `<filter id="cardshadow" x="-30%%" y="-30%%" width="160%%" height="160%%">`+
				`<feDropShadow dx="0" dy="%s" stdDeviation="%s" flood-opacity="0.35"/></filter>`+"\n",
				fnum(l.ch*0.45), fnum(l.ch*0.6))
		}
	}
	if f != nil && f.EmbedFont && len(f.FontData) > 0 {
		fmt.Fprintf(b, `<style>@font-face{font-family:%q;src:url(data:font/ttf;base64,%s);}</style>`+"\n",
			svgPrimaryFamily(family), base64.StdEncoding.EncodeToString(f.FontData))
	}
	fmt.Fprintf(b, `<style>text{font-family:%s;font-size:%spx;white-space:pre;}</style>`+"\n",
		xmlEscape(family), fnum(svgFontSize))
	b.WriteString("</defs>\n")
}

// svgPrimaryFamily is the first name in a CSS font stack, so the embedded
// face registers under the name the stack asks for.
func svgPrimaryFamily(stack string) string {
	name := stack
	if i := strings.IndexByte(stack, ','); i >= 0 {
		name = stack[:i]
	}
	return strings.Trim(strings.TrimSpace(name), `"'`)
}

func writeSVGTitleBar(b *strings.Builder, g *Grid, f *Frame, l layout, family string) {
	barY := l.cardY
	// A hairline under the bar, quiet against the card.
	rule := Mix(g.BG, g.FG, 0.12)
	fmt.Fprintf(b, `<rect x="%s" y="%s" width="%s" height="1" fill="%s"/>`+"\n",
		fnum(l.cardX), fnum(barY+l.titleH-1), fnum(l.cardW), Hex(rule))

	cy := barY + l.titleH/2
	r := l.titleH * 0.17
	gap := r * 3.2
	cx := l.cardX + l.inset + r
	switch f.Controls {
	case ControlsMacOS:
		for i, c := range [3]string{"#ff5f57", "#febc2e", "#28c840"} {
			fmt.Fprintf(b, `<circle cx="%s" cy="%s" r="%s" fill="%s"/>`+"\n",
				fnum(cx+float64(i)*gap), fnum(cy), fnum(r), c)
		}
	case ControlsGlyphs:
		marks := []string{f.CloseGlyph, f.MinimizeGlyph, f.MaximizeGlyph}
		for i, m := range marks {
			if m == "" {
				continue
			}
			fmt.Fprintf(b, `<text x="%s" y="%s" text-anchor="middle" fill="%s">%s</text>`+"\n",
				fnum(cx+float64(i)*gap), fnum(cy+svgFontSize*0.34), Hex(f.Accent), xmlEscape(m))
		}
	case ControlsNone:
	}
	if f.Title != "" {
		fmt.Fprintf(b, `<text x="%s" y="%s" text-anchor="middle" fill="%s">%s</text>`+"\n",
			fnum(l.cardX+l.cardW/2), fnum(cy+svgFontSize*0.34),
			Hex(Mix(g.FG, g.BG, 0.3)), xmlEscape(f.Title))
	}
	_ = family
}

func writeSVGRun(b *strings.Builder, r run, l layout) {
	x := float64(r.col) * l.cw
	y := float64(r.row) * l.ch
	w := float64(r.cells) * l.cw
	c := r.cell
	fill := Hex(c.FG)
	opacity := ""
	if c.Faint {
		opacity = ` fill-opacity="0.55"`
	}

	if r.procedural != 0 {
		paths, _ := proceduralPaths(r.procedural, l.cw, l.ch)
		for _, p := range paths {
			op := opacity
			if p.opacity > 0 {
				op = fmt.Sprintf(` fill-opacity="%s"`, fnum(p.opacity))
			}
			fmt.Fprintf(b, `<path d="%s" fill="%s"%s/>`+"\n", pathData(p, x, y), fill, op)
		}
		writeSVGDecor(b, r, x, y, w, l)
		return
	}
	if !r.isBlank() {
		text := strings.TrimRight(r.text, " ")
		if text != "" {
			style := ""
			if c.Bold {
				style += ` font-weight="bold"`
			}
			if c.Italic {
				style += ` font-style="italic"`
			}
			if c.Link != "" {
				fmt.Fprintf(b, `<a href="%s">`, xmlEscape(c.Link))
			}
			fmt.Fprintf(b, `<text x="%s" y="%s" textLength="%s" lengthAdjust="spacingAndGlyphs" fill="%s"%s%s xml:space="preserve">%s</text>`+"\n",
				fnum(x), fnum(y+l.ch*0.5+svgFontSize*0.32),
				fnum(float64(cellsOf(text, r))*l.cw), fill, style, opacity, xmlEscape(text))
			if c.Link != "" {
				b.WriteString("</a>")
			}
		}
	}
	writeSVGDecor(b, r, x, y, w, l)
}

// cellsOf is the cell count of the run text after right-trimming spaces.
func cellsOf(trimmed string, r run) int {
	if r.wide {
		return 2
	}
	// One cluster per cell in a narrow run, so trimming spaces off the end
	// removes exactly one cell per space.
	removed := len([]rune(r.text)) - len([]rune(trimmed))
	// Note: multi-rune clusters make rune counting approximate; runs are
	// split so narrow multi-rune clusters are rare, and textLength keeps
	// the columns honest regardless.
	return r.cells - removed
}

// writeSVGDecor draws underline styles and strikethrough as geometry.
func writeSVGDecor(b *strings.Builder, r run, x, y, w float64, l layout) {
	c := r.cell
	t := l.ch / 14
	if t < 1 {
		t = 1
	}
	uy := y + l.ch - t*2
	line := func(yy, tt float64) {
		fmt.Fprintf(b, `<rect x="%s" y="%s" width="%s" height="%s" fill="%s"/>`+"\n",
			fnum(x), fnum(yy), fnum(w), fnum(tt), Hex(c.FG))
	}
	switch c.Underline {
	case UnderlineSingle:
		line(uy, t)
	case UnderlineDouble:
		line(uy-t*1.8, t)
		line(uy, t)
	case UnderlineCurly:
		fmt.Fprintf(b, `<path d="%s" stroke="%s" stroke-width="%s" fill="none"/>`+"\n",
			curlyPath(x, uy, w, t), Hex(c.FG), fnum(t))
	case UnderlineDotted:
		fmt.Fprintf(b, `<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-width="%s" stroke-dasharray="%s %s"/>`+"\n",
			fnum(x), fnum(uy+t/2), fnum(x+w), fnum(uy+t/2), Hex(c.FG), fnum(t), fnum(t), fnum(t*1.5))
	case UnderlineDashed:
		fmt.Fprintf(b, `<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-width="%s" stroke-dasharray="%s %s"/>`+"\n",
			fnum(x), fnum(uy+t/2), fnum(x+w), fnum(uy+t/2), Hex(c.FG), fnum(t), fnum(t*3), fnum(t*2))
	}
	if c.Strike {
		line(y+l.ch*0.5-t/2, t)
	}
}

// curlyPath is one wave period per cell, quadratic segments.
func curlyPath(x, y, w, t float64) string {
	var b strings.Builder
	amp := t * 1.2
	step := w
	if w > 0 {
		step = w / float64(int(w/6)+1)
	}
	fmt.Fprintf(&b, "M%s %s", fnum(x), fnum(y))
	up := true
	for cx := x; cx < x+w-0.01; cx += step {
		cy := y + amp
		if up {
			cy = y - amp
		}
		fmt.Fprintf(&b, " Q%s %s %s %s", fnum(cx+step/2), fnum(cy), fnum(min(cx+step, x+w)), fnum(y))
		up = !up
	}
	return b.String()
}

// pathData serializes a gpath at a cell origin.
func pathData(p gpath, ox, oy float64) string {
	var b strings.Builder
	for _, o := range p.ops {
		switch o.op {
		case 'M':
			fmt.Fprintf(&b, "M%s %s", fnum(ox+o.x), fnum(oy+o.y))
		case 'L':
			fmt.Fprintf(&b, "L%s %s", fnum(ox+o.x), fnum(oy+o.y))
		case 'Q':
			fmt.Fprintf(&b, "Q%s %s %s %s", fnum(ox+o.c1x), fnum(oy+o.c1y), fnum(ox+o.x), fnum(oy+o.y))
		case 'C':
			fmt.Fprintf(&b, "C%s %s %s %s %s %s", fnum(ox+o.c1x), fnum(oy+o.c1y),
				fnum(ox+o.c2x), fnum(oy+o.c2y), fnum(ox+o.x), fnum(oy+o.y))
		case 'Z':
			b.WriteString("Z")
		}
	}
	return b.String()
}

// fnum formats a coordinate with just enough precision for a crisp grid.
func fnum(f float64) string {
	s := fmt.Sprintf("%.2f", f)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" || s == "-" {
		return "0"
	}
	return s
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
