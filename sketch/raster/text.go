package raster

import (
	"bytes"
	"compress/gzip"
	"embed"
	"io"
	"strings"
	"sync"

	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// Arimo, regular and bold, has Arial's metrics, which pele's width table
// measures in, so text fits the boxes it was measured for. Go Mono draws
// monospace text. fonts/NOTICE has their licences.
//
//go:embed fonts/*.ttf.gz
var fontFiles embed.FS

type face struct {
	font *sfnt.Font
	upem float64
	mu   sync.Mutex
	buf  sfnt.Buffer
	// glyphs caches outlines in font units, y down, by rune.
	glyphs map[rune]*glyph
	kerns  map[[2]sfnt.GlyphIndex]float64
}

type glyph struct {
	ok      bool
	index   sfnt.GlyphIndex
	advance float64
	outline path
}

var (
	facesOnce sync.Once
	faces     [3]*face // regular, bold, mono
	facesErr  error
)

func loadFace(name string) (*face, error) {
	data, err := fontFiles.ReadFile(name)
	if err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}
	f, err := sfnt.Parse(raw)
	if err != nil {
		return nil, err
	}
	return &face{font: f, upem: float64(f.UnitsPerEm()), glyphs: map[rune]*glyph{}, kerns: map[[2]sfnt.GlyphIndex]float64{}}, nil
}

func loadFaces() error {
	facesOnce.Do(func() {
		for i, name := range []string{"fonts/Arimo-Regular.ttf.gz", "fonts/Arimo-Bold.ttf.gz", "fonts/Go-Mono.ttf.gz"} {
			if faces[i], facesErr = loadFace(name); facesErr != nil {
				return
			}
		}
	})
	return facesErr
}

// glyphFor is the outline and advance of r in font units.
func (f *face) glyphFor(r rune) *glyph {
	f.mu.Lock()
	defer f.mu.Unlock()
	if g, ok := f.glyphs[r]; ok {
		return g
	}
	g := &glyph{}
	f.glyphs[r] = g
	idx, err := f.font.GlyphIndex(&f.buf, r)
	if err != nil || idx == 0 {
		return g
	}
	ppem := fixed.Int26_6(f.upem * 64)
	adv, err := f.font.GlyphAdvance(&f.buf, idx, ppem, 0)
	if err != nil {
		return g
	}
	segs, err := f.font.LoadGlyph(&f.buf, idx, ppem, nil)
	if err != nil {
		return g
	}
	g.ok = true
	g.index = idx
	g.advance = float64(adv) / 64
	p := func(v fixed.Point26_6) pt { return pt{float64(v.X) / 64, float64(v.Y) / 64} }
	var cur, start pt
	open := false
	for _, s := range segs {
		switch s.Op {
		case sfnt.SegmentOpMoveTo:
			if open {
				g.outline.close()
			}
			cur = p(s.Args[0])
			start = cur
			g.outline.moveTo(cur)
			open = true
		case sfnt.SegmentOpLineTo:
			cur = p(s.Args[0])
			g.outline.lineTo(cur)
		case sfnt.SegmentOpQuadTo:
			e := p(s.Args[1])
			g.outline.quadTo(cur, p(s.Args[0]), e)
			cur = e
		case sfnt.SegmentOpCubeTo:
			e := p(s.Args[2])
			g.outline.cubicTo(p(s.Args[0]), p(s.Args[1]), e)
			cur = e
		}
	}
	if open {
		g.outline.close()
	}
	_ = start
	return g
}

// kern is the kerning between two glyphs of the face, in font units.
func (f *face) kern(a, b *glyph) float64 {
	key := [2]sfnt.GlyphIndex{a.index, b.index}
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.kerns[key]; ok {
		return v
	}
	k, err := f.font.Kern(&f.buf, a.index, b.index, fixed.Int26_6(f.upem*64), 0)
	v := 0.0
	if err == nil {
		v = float64(k) / 64
	}
	f.kerns[key] = v
	return v
}

// runWidth is the advance of a run of text, kerned.
func (st state) runWidth(text string) float64 {
	width := 0.0
	var pf *face
	var pg *glyph
	for _, ru := range text {
		f, g := st.glyphOf(ru)
		if g != nil && pg != nil && f == pf {
			width += f.kern(pg, g) * st.fontSize / f.upem
		}
		width += st.advance(ru)
		pf, pg = f, g
	}
	return width
}

// isWide matches the width table: wide characters take a whole em.
func isWide(c rune) bool {
	return (c >= 0x1100 && c <= 0x115f) || (c >= 0x2e80 && c <= 0xa4cf) || (c >= 0xac00 && c <= 0xd7a3) ||
		(c >= 0xf900 && c <= 0xfaff) || (c >= 0xfe30 && c <= 0xfe4f) || (c >= 0xff00 && c <= 0xff60) ||
		(c >= 0xffe0 && c <= 0xffe6) || c > 0xffff
}

type textRun struct {
	text string
	st   state
}

type textChunk struct {
	x, y       float64
	hasX, hasY bool
	anchor     string
	runs       []textRun
}

func collapseSpace(s string) string {
	s = strings.NewReplacer("\r", "", "\n", "", "\t", " ").Replace(s)
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return s
}

func (st state) face() *face {
	switch {
	case st.mono:
		return faces[2]
	case st.bold:
		return faces[1]
	}
	return faces[0]
}

// glyphOf finds a glyph for r in the run's face, then in the others.
func (st state) glyphOf(r rune) (*face, *glyph) {
	f := st.face()
	if g := f.glyphFor(r); g.ok {
		return f, g
	}
	for _, other := range faces {
		if other != f {
			if g := other.glyphFor(r); g.ok {
				return other, g
			}
		}
	}
	return nil, nil
}

func (st state) advance(r rune) float64 {
	f, g := st.glyphOf(r)
	if g == nil {
		if r < 32 {
			return 0
		}
		if isWide(r) {
			return st.fontSize
		}
		return st.fontSize * 0.58
	}
	return g.advance * st.fontSize / f.upem
}

// text lays out a text element: each x or y starts a new chunk, and the
// text-anchor of a chunk places it.
func (r *renderer) text(n *node, st state) {
	first := &textChunk{anchor: st.anchor}
	if v, ok := n.attr("x"); ok {
		first.x, first.hasX = lengthOf(firstNumber(v), 0), true
	}
	if v, ok := n.attr("y"); ok {
		first.y, first.hasY = lengthOf(firstNumber(v), 0), true
	}
	chunks := []*textChunk{first}
	var walk func(n *node, st state, depth int)
	walk = func(n *node, st state, depth int) {
		if depth > 32 {
			return
		}
		for _, c := range n.children {
			if c.name == "" {
				t := c.text
				if !st.preserve {
					t = collapseSpace(t)
				}
				cur := chunks[len(chunks)-1]
				cur.runs = append(cur.runs, textRun{t, st})
				continue
			}
			if c.name != "tspan" && c.name != "a" {
				continue
			}
			cst := r.apply(c, st)
			xs, hasX := c.attr("x")
			ys, hasY := c.attr("y")
			if hasX || hasY {
				ch := &textChunk{anchor: cst.anchor, hasX: hasX, hasY: hasY}
				ch.x, ch.y = lengthOf(firstNumber(xs), 0), lengthOf(firstNumber(ys), 0)
				chunks = append(chunks, ch)
			}
			walk(c, cst, depth+1)
		}
	}
	walk(n, st, 0)
	if !st.preserve {
		// Spaces at the start and the end of the text are dropped.
		trimRuns(chunks, true)
		trimRuns(chunks, false)
	}
	penX, penY := 0.0, 0.0
	for _, ch := range chunks {
		if ch.hasX {
			penX = ch.x
		}
		if ch.hasY {
			penY = ch.y
		}
		width := 0.0
		for _, run := range ch.runs {
			width += run.st.runWidth(run.text)
		}
		switch ch.anchor {
		case "middle":
			penX -= width / 2
		case "end":
			penX -= width
		}
		for _, run := range ch.runs {
			var p path
			var pf *face
			var pg *glyph
			for _, ru := range run.text {
				if r.budget--; r.budget < 0 {
					return
				}
				f, g := run.st.glyphOf(ru)
				if g == nil {
					adv := run.st.advance(ru)
					if ru > ' ' {
						// A character no embedded face has: a box of the width pele measured.
						s := run.st.fontSize
						x0, x1 := penX+adv*0.12, penX+adv*0.88
						y0, y1 := penY-s*0.7, penY+s*0.05
						t := s * 0.07
						for _, r := range [][4]float64{{x0, y0, x1, y0 + t}, {x0, y1 - t, x1, y1}, {x0, y0, x0 + t, y1}, {x1 - t, y0, x1, y1}} {
							p.moveTo(pt{r[0], r[1]})
							p.lineTo(pt{r[2], r[1]})
							p.lineTo(pt{r[2], r[3]})
							p.lineTo(pt{r[0], r[3]})
							p.close()
						}
					}
					penX += adv
					pf, pg = nil, nil
					continue
				}
				k := run.st.fontSize / f.upem
				if pg != nil && pf == f {
					penX += f.kern(pg, g) * k
				}
				pf, pg = f, g
				shear := 0.0
				if run.st.italic {
					shear = 0.2
				}
				at := func(q pt) pt { return pt{penX + (q.x-shear*q.y)*k, penY + q.y*k} }
				for _, s := range g.outline {
					switch s.kind {
					case segMove:
						p.moveTo(at(s.p[0]))
					case segLine:
						p.lineTo(at(s.p[0]))
					case segCubic:
						p.cubicTo(at(s.p[0]), at(s.p[1]), at(s.p[2]))
					case segClose:
						p.close()
					}
				}
				penX += g.advance * k
			}
			st := run.st
			if r.readable && !st.authored {
				st = r.readableText(p, st)
			}
			if r.probe != nil {
				r.probe(r, p, st)
			}
			r.shape(p, st, true)
		}
	}
}

func firstNumber(s string) string {
	if f := strings.Fields(strings.ReplaceAll(s, ",", " ")); len(f) > 0 {
		return f[0]
	}
	return ""
}

func trimRuns(chunks []*textChunk, start bool) {
	if start {
		for _, ch := range chunks {
			for i := range ch.runs {
				ch.runs[i].text = strings.TrimLeft(ch.runs[i].text, " ")
				if ch.runs[i].text != "" {
					return
				}
			}
		}
		return
	}
	for c := len(chunks) - 1; c >= 0; c-- {
		ch := chunks[c]
		for i := len(ch.runs) - 1; i >= 0; i-- {
			ch.runs[i].text = strings.TrimRight(ch.runs[i].text, " ")
			if ch.runs[i].text != "" {
				return
			}
		}
	}
}
