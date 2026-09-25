package app

import (
	"image/color"

	"github.com/Gaurav-Gosain/tuios/internal/diffview"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/review"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The review overlay's colours, and what it keeps of a diff between frames.
//
// The overlay covers the whole screen and every cell of it is painted: a
// cell left on the default background would take the desktop's ground, or
// the host terminal's, through a full-screen view. The ground is the one a
// pane shows code on, so the diff reads like the file in the theme the
// person chose:
//
//   - the pane background (appearance.pane_background, or the default for
//     every surface) when one is set;
//   - otherwise the theme's own background, when a theme is set;
//   - otherwise the chrome's surface, the ground every other overlay uses.
//
// The chrome palette's inks were picked against its own dark surface. On
// any other ground they are measured again, and lifted where they would not
// read, so a light theme gets dark text rather than the chrome's cream.

// reviewLook is the palette and diff theme for one ground, kept until the
// theme or the pane background changes.
type reviewLook struct {
	key string
	pal overlay.Palette
	dv  *diffview.Theme
	// hunkBg and noteBg are the grounds of a hunk header row and a note row.
	hunkBg, noteBg color.Color
}

// reviewLook is the overlay's colours for this frame.
func (m *OS) reviewLook() *reviewLook {
	g := m.paneGround()
	key := theme.CurrentThemeID() + "\x00" + g.key
	if l := m.review.look; l != nil && l.key == key {
		return l
	}
	l := newReviewLook(g)
	l.key = key
	m.review.look = l
	return l
}

// newReviewLook works the overlay's colours out for a pane ground.
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
	if ground != solidColor(pal.Surface) {
		pal = reviewPaletteOn(pal, ground, solidColor(fg))
	}
	syntax := diffview.DefaultSyntax()
	if t != nil {
		var slots [16]color.Color
		for i, c := range theme.ANSIOrder(t) {
			if c != nil {
				slots[i] = c
			}
		}
		syntax = diffview.SyntaxFromANSI(slots)
	}
	l := &reviewLook{pal: pal}
	l.dv = diffview.NewTheme(diffview.Palette{
		Ground: ground,
		Fg:     pal.Fg,
		Accent: pal.Accent,
		Add:    pal.Success,
		Delete: pal.Warn,
		Syntax: syntax,
	})
	l.hunkBg = overlay.MixColors(ground, pal.Info, 0.12)
	l.noteBg = overlay.MixColors(ground, pal.Warning, 0.10)
	return l
}

// reviewPaletteOn is the chrome palette measured again on another ground:
// the ground becomes the surface, and each ink is kept where it reads on it
// and lifted where it does not.
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
	return p
}

// reviewHighlightChunk is how many lines of a hunk are tokenised at a time.
// chroma takes around a tenth of a millisecond a line, so a hunk of five
// thousand lines read whole would hold the first frame for half a second;
// a chunk is the few milliseconds the rows on screen need. A comment or a
// string that crosses a chunk's edge is read from that edge, which costs
// its colour on the lines past it and nothing else.
const reviewHighlightChunk = 64

// reviewHunkLook is what the overlay keeps of one hunk: its lines as drawn,
// the side by side pairing, the changed part of each changed line, and, for
// each chunk of lines that has been on screen, the colours of its code.
type reviewHunkLook struct {
	text    []string
	kinds   []diffview.Kind
	pairs   []diffview.Pair
	changed []diffview.Range
	spans   [][]diffview.Span
	// done marks the chunks tokenised.
	done []bool
}

// reviewHunk is the kept look of a file's hunk, made on first use. Line
// line's chunk is tokenised as well when line is not negative, which is the
// one expensive step, and only the drawing of a row on screen asks for it.
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

// highlight tokenises lines from to to of the hunk, each side as one text:
// the old side's lines with the old path, the new side's with the new.
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
		// The new side is read second, so a line both sides have takes its
		// colours from the new side.
		for j, spans := range diffview.Highlight(side.path, text) {
			hl.spans[idx[j]] = spans
		}
	}
}
