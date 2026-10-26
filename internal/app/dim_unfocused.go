package app

import (
	"image/color"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"tuios.dev/vt"
)

// Dimming an unfocused pane's content is the one appearance question the frame
// could not answer. A pane says it is focused with the colour of its border and
// nothing else, which is a one-cell rule at the edge of a rectangle; on a wide
// screen full of panes that is the smallest signal in the frame carrying the
// most important fact in it. wezterm's inactive_pane_hsb and tmux's
// window-active-style both answer it by quieting the content instead, which is
// most of the pixels and so reads without being looked for.
//
// It composes with zen_mode rather than duplicating it: zen takes the chrome
// away, this quiets the content, and a user can want either without the other.
//
// What is dimmed is exactly the guest's own cells. Everything the contrast
// model answers for is untouched, because none of it is drawn here: the border,
// the title bar and its controls, the scrollbar, the rail, the dock, the
// notifications and every overlay are composed elsewhere and go on being
// measured against ContrastFloor, MarkFloor and Structure. A dim that reached
// them would be a setting that quietly lowers the floors this whole area exists
// to hold, which is why it is applied at the cell loop rather than to the
// finished pane.
//
// The content itself has no floor. It is the user's text in the user's
// programs, and a user who wants it nearly gone is entitled to that; the cap
// below only stops a pane from being erased outright, where there would be
// nothing left to tell the setting had worked.

// paneDim is the dim that applies to one pane this frame: the configured
// amount for an unfocused pane, and none for the focused one.
//
// It doubles as the content cache's key. renderTerminal caches the string it
// built, and FocusWindow deliberately gives the pane being left the lighter
// invalidation that keeps that string, so without a key the pane you just
// stepped away from would keep serving the undimmed frame until its guest next
// wrote something. Keying the cache on the dim holds for every focus path,
// because it is checked where the cache is used rather than where focus
// moves.
func paneDim(isFocused bool, s *config.Settings) int {
	if isFocused {
		return 0
	}
	return s.DimUnfocused
}

// dimCell fills dst with src carried toward the pane's ground and returns it,
// or returns src unchanged when there is nothing to dim.
//
// dimText says whether the text is carried toward the ground along with the
// background. False, appearance.dim_foreground off, leaves the guest's text in
// its own colour and quiets only the ground behind it.
//
// The scratch cell is the caller's because this runs once per style run rather
// than once per cell: the batching loop compares undimmed cells to decide where
// a run ends, so a run's style is built once and the blend is paid once for it.
// A run is typically a whole word or a whole line.
//
// fg and bg are the colours the host paints a cell that names none with, and
// pal is the host's sixteen when no theme is set (nil with a theme, whose
// sixteen the emulator has already written into the cells as RGB). A colour
// this cannot read channels off, because the host never said what it is,
// is not guessed at: the text takes SGR 2 instead, which the host dims by an
// amount of its own choosing.
//
// A cell with no background of its own keeps none. It already shows the
// ground the dim carries toward, and spelling that ground out would put an
// opaque block over a terminal drawn with a translucent background.
func dimCell(dst, src *uv.Cell, fg, bg color.Color, pal *[16]color.Color, t float64, dimText bool, memo *blendMemo) *uv.Cell {
	if src == nil {
		return src
	}
	// A kitty placeholder cell is not text and its colour is not a colour: the
	// foreground is the image's id, and blending it renames the image to one
	// the host has never heard of, which draws nothing. The cell is a pixel of
	// a picture, so there is nothing here to dim in the first place.
	if vt.IsKittyPlaceholder(src.Content) {
		return src
	}
	// isNilColor rather than == nil: a cell's style colour can be an interface
	// holding a nil pointer, and color.Color's RGBA has a value receiver, so
	// calling it through one panics rather than returning zeros. Every RGBA
	// call on a cell style in this package screens for that first.
	cellFg, cellBg := hostInk(src.Style.Fg, fg, pal), hostInk(src.Style.Bg, bg, pal)
	ownBg := !isNilColor(src.Style.Bg)
	if !dimText && !ownBg {
		return src
	}
	*dst = *src
	if dimText {
		// The fg goes to the cell's own ground where it has one, so a cell
		// painted on a block of colour dims into that block rather than into
		// the pane behind it and keeps the block readable as a block.
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

// hostInk is c as the host paints it: def for a cell that names no colour,
// the host's own slot for one of the sixteen when pal is set, and nil when
// the host never said what that slot is. Any other colour is its own answer.
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

// dimGlyphCell is dimCell for a cell of an image drawn as glyphs. Both of its
// colours are picture colours, so both go toward the pane's ground by the same
// amount. dimCell carries the foreground toward the cell's own background,
// which is right for text on a block of colour and wrong here: in a flat area
// the two colours are nearly equal, so the foreground hardly moves while the
// background moves to the pane, and the area breaks into light and dark
// sub-blocks. A cell that names no colour stays on the pane's ground and needs
// nothing.
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

// paneDimGround is what an unfocused pane's cells are dimmed toward and
// from: the background the dim carries toward, the foreground a cell that
// names no colour is painted with, and with no theme the host's sixteen.
//
// A painted pane background is the pane's own background, so it is what the
// dim carries toward when one is set, theme or not. With a theme the rest is
// the theme's own pair. With no theme it is what the host terminal said about
// its colours (see host_colors.go). A colour the host did not answer for is
// nil, and dimCell draws text it cannot blend with SGR 2 instead.
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

// dimGround is the pair a dimmed cell is carried toward: the pane's own
// background, and the foreground standing in for a cell that named none. Both
// are nil when no theme is set, which is what leaves those cells alone.
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

// blendMemo keeps the last blends a pane render asked for. The blend is in
// OKLab (see overlay.MixColors), which costs a few cube roots a call, and a
// pane's runs repeat a handful of colours, so a render pays for each pair once.
// It is direct-mapped and lives on the render's stack, so it needs no lock and
// allocates nothing; a hit also hands back the colour already boxed, which the
// uncached blend allocated for on every run.
type blendMemo struct {
	key [blendMemoSize]uint64
	out [blendMemoSize]color.Color
	t   float64
}

const blendMemoSize = 64

// mix is blendColors behind the memo. The memo holds one blend fraction, the
// pane's, and starts over if asked for another.
func (bm *blendMemo) mix(a, b color.Color, t float64) color.Color {
	if bm == nil {
		return blendColors(a, b, t)
	}
	if bm.t != t {
		*bm = blendMemo{t: t}
	}
	// The tag bit keeps a real key of zero, black onto black, apart from an
	// empty slot.
	key := 1<<63 | uint64(packColor8(a))<<24 | uint64(packColor8(b))
	i := (key ^ key>>17 ^ key>>31) % blendMemoSize
	if bm.key[i] == key {
		return bm.out[i]
	}
	c := blendColors(a, b, t)
	bm.key[i], bm.out[i] = key, c
	return c
}
