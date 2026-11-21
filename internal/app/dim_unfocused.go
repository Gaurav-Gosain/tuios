package app

import (
	"image/color"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"tuios.dev/vt"
)

func paneDim(isFocused bool, s *config.Settings) int {
	if isFocused {
		return 0
	}
	return s.DimUnfocused
}

func dimCell(dst, src *uv.Cell, fg, bg color.Color, pal *[16]color.Color, t float64, dimText bool, memo *blendMemo) *uv.Cell {
	if src == nil {
		return src
	}
	if vt.IsKittyPlaceholder(src.Content) {
		return src
	}
	cellFg, cellBg := hostInk(src.Style.Fg, fg, pal), hostInk(src.Style.Bg, bg, pal)
	ownBg := !isNilColor(src.Style.Bg)
	if !dimText && !ownBg {
		return src
	}
	*dst = *src
	if dimText {
		toward := cellBg
		if isNilColor(toward) {
			toward = bg
		}
		if !isNilColor(cellFg) && !isNilColor(toward) {
			dst.Style.Fg = memo.mix(cellFg, toward, t)
		} else {
			dst.Style.Attrs |= uv.AttrFaint
		}
	}
	if ownBg && !isNilColor(cellBg) && !isNilColor(bg) {
		dst.Style.Bg = memo.mix(cellBg, bg, t)
	}
	return dst
}

func hostInk(c, def color.Color, pal *[16]color.Color) color.Color {
	if isNilColor(c) {
		return def
	}
	if pal == nil {
		return c
	}
	switch v := c.(type) {
	case ansi.BasicColor:
		return pal[v&15]
	case ansi.IndexedColor:
		if v < 16 {
			return pal[v]
		}
	}
	return c
}

func dimGlyphCell(dst, src *uv.Cell, bg color.Color, t float64, memo *blendMemo) *uv.Cell {
	if src == nil || isNilColor(bg) {
		return src
	}
	cellFg, cellBg := src.Style.Fg, src.Style.Bg
	if isNilColor(cellFg) && isNilColor(cellBg) {
		return src
	}
	*dst = *src
	if !isNilColor(cellFg) {
		dst.Style.Fg = memo.mix(cellFg, bg, t)
	}
	if !isNilColor(cellBg) {
		dst.Style.Bg = memo.mix(cellBg, bg, t)
	}
	return dst
}

func (m *OS) paneDimGround() (fg, bg color.Color, pal *[16]color.Color) {
	g := m.paneGround()
	if g.on() {
		fg, bg = g.fg, g.bg
	}
	if theme.Current() != nil {
		tfg, tbg := dimGround()
		if bg == nil {
			bg = tbg
		}
		if fg == nil {
			fg = tfg
		}
		return fg, bg, nil
	}
	if bg == nil {
		bg = m.host.bg
	}
	if fg == nil {
		fg = m.host.fg
	}
	return fg, bg, &m.host.ansi
}

func dimGround() (fg, bg color.Color) {
	if theme.CurrentThemeID() == "" {
		return nil, nil
	}
	fg, bg = theme.TerminalFg(), theme.TerminalBg()
	if isNilColor(fg) || isNilColor(bg) {
		return nil, nil
	}
	return fg, bg
}

type blendMemo struct {
	key [blendMemoSize]uint64
	out [blendMemoSize]color.Color
	t   float64
}

const blendMemoSize = 64

func (bm *blendMemo) mix(a, b color.Color, t float64) color.Color {
	if bm == nil {
		return blendColors(a, b, t)
	}
	if bm.t != t {
		*bm = blendMemo{t: t}
	}
	key := 1<<63 | uint64(packColor8(a))<<24 | uint64(packColor8(b))
	i := (key ^ key>>17 ^ key>>31) % blendMemoSize
	if bm.key[i] == key {
		return bm.out[i]
	}
	c := blendColors(a, b, t)
	bm.key[i], bm.out[i] = key, c
	return c
}
