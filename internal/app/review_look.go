package app

import (
	"image/color"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/diffview"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/review"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

type reviewLook struct {
	key            string
	pal            overlay.Palette
	dv             *diffview.Theme
	hunkBg, noteBg color.Color
}

func diffTheme(pal overlay.Palette, ground color.Color) *diffview.Theme {
	syntax := diffview.DefaultSyntax()
	if t := theme.Current(); t != nil {
		var slots [16]color.Color
		for i, c := range theme.ANSIOrder(t) {
			if c != nil {
				slots[i] = c
			}
		}
		syntax = diffview.SyntaxFromANSI(slots)
	}
	return diffview.NewTheme(diffview.Palette{Depth: pal.Depth, Ground: ground, Fg: pal.Fg, Accent: pal.Accent,
		Add: pal.Success, Delete: pal.Warn, Syntax: syntax})
}

func (m *OS) reviewLook() *reviewLook {
	g := m.paneGround()
	key := strconv.FormatUint(theme.Generation(), 10) + "\x00" + g.key + "\x00" + theme.Depth().String()
	if l := m.review.look; l != nil && l.key == key {
		return l
	}
	l := newReviewLook(g)
	l.key = key
	m.review.look = l
	return l
}

func newReviewLook(g ground) *reviewLook {
	pal := theme.UI()
	t := theme.Current()
	ground, fg := pal.Surface, pal.Fg
	switch {
	case g.on():
		ground = g.bg
		switch {
		case g.fg != nil:
			fg = g.fg
		case t != nil && !isNilColor(theme.TerminalFg()):
			fg = theme.TerminalFg()
		default:
			fg = overlay.ContrastText(ground)
		}
	case t != nil && !isNilColor(theme.TerminalBg()):
		ground = theme.TerminalBg()
		if c := theme.TerminalFg(); !isNilColor(c) {
			fg = c
		}
	}
	ground = solidColor(ground)
	switch {
	case pal.Depth == overlay.Depth16:
		ground = pal.Surface
	case ground != solidColor(pal.Surface):
		pal = reviewPaletteOn(pal, ground, solidColor(fg))
	}
	l := &reviewLook{pal: pal}
	l.dv = diffTheme(pal, ground)
	l.hunkBg, l.noteBg = pal.Surface, pal.Surface
	if pal.Depth != overlay.Depth16 {
		l.hunkBg = overlay.Shown(overlay.MixColors(ground, pal.Info, 0.12))
		l.noteBg = overlay.Shown(overlay.MixColors(ground, pal.Warning, 0.10))
	}
	return l
}

func reviewPaletteOn(pal overlay.Palette, ground, fg color.Color) overlay.Palette {
	p := pal
	p.Canvas, p.Panel, p.Surface = ground, ground, ground
	p.Fg = overlay.Readable(fg, ground)
	p.FgDim = overlay.ReadableAt(overlay.MixColors(p.Fg, ground, 0.3), ground, overlay.ContrastFloor)
	p.FgMute = overlay.ReadableAt(overlay.MixColors(p.Fg, ground, 0.5), ground, overlay.MarkFloor)
	p.Accent = overlay.ReadableAt(pal.Accent, ground, overlay.MarkFloor)
	p.AccentBright = overlay.Readable(pal.AccentBright, ground)
	p.Selected = p.Accent
	p.Success = overlay.Readable(pal.Success, ground)
	p.Warn = overlay.Readable(pal.Warn, ground)
	p.Info = overlay.Readable(pal.Info, ground)
	p.Warning = overlay.Readable(pal.Warning, ground)
	p.RowSel = overlay.MixColors(ground, pal.Accent, 0.22)
	p.Card = overlay.MixColors(ground, p.Fg, 0.08)
	p.RowSelQuiet, p.Hover = nil, nil
	return overlay.Derive(p)
}

const reviewHighlightChunk = 64

type reviewHunkLook struct {
	text    []string
	kinds   []diffview.Kind
	pairs   []diffview.Pair
	changed []diffview.Range
	spans   [][]diffview.Span
	done    []bool
}

func (r *reviewState) reviewHunk(f *review.File, h, line int) *reviewHunkLook {
	if r.hunks == nil {
		r.hunks = map[*review.File][]*reviewHunkLook{}
	}
	looks := r.hunks[f]
	if looks == nil {
		looks = make([]*reviewHunkLook, len(f.Hunks))
		r.hunks[f] = looks
	}
	hl := looks[h]
	if hl == nil {
		lines := f.Hunks[h].Lines
		n := len(lines)
		hl = &reviewHunkLook{
			text:    make([]string, n),
			kinds:   make([]diffview.Kind, n),
			changed: make([]diffview.Range, n),
			spans:   make([][]diffview.Span, n),
			done:    make([]bool, (n+reviewHighlightChunk-1)/reviewHighlightChunk),
		}
		for i, ln := range lines {
			hl.text[i] = reviewText(ln.Text)
			hl.kinds[i] = lineKind(ln)
		}
		hl.pairs = diffview.Pairs(hl.kinds)
		for _, p := range hl.pairs {
			if p.Left >= 0 && p.Right >= 0 && p.Left != p.Right {
				hl.changed[p.Left], hl.changed[p.Right] = diffview.Changed(hl.text[p.Left], hl.text[p.Right])
			}
		}
		looks[h] = hl
	}
	if line >= 0 && line < len(hl.text) {
		if c := line / reviewHighlightChunk; !hl.done[c] {
			hl.done[c] = true
			hl.highlight(f, c*reviewHighlightChunk, min((c+1)*reviewHighlightChunk, len(hl.text)))
		}
	}
	return hl
}

func (hl *reviewHunkLook) highlight(f *review.File, from, to int) {
	if !diffview.Enabled {
		return
	}
	oldPath := f.Path
	if f.OldPath != "" {
		oldPath = f.OldPath
	}
	for _, side := range []struct {
		path string
		skip diffview.Kind
	}{{oldPath, diffview.Add}, {f.Path, diffview.Delete}} {
		var idx []int
		var text []string
		for i := from; i < to; i++ {
			if hl.kinds[i] != side.skip {
				idx = append(idx, i)
				text = append(text, hl.text[i])
			}
		}
		for j, spans := range diffview.Highlight(side.path, text) {
			hl.spans[idx[j]] = spans
		}
	}
}
