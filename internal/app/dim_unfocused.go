package app

import (
	"image/color"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
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

// dimBlend is the fraction of the way to the ground an unfocused cell is
// carried, for the configured percentage.
func dimBlend() float64 { return float64(config.DimUnfocused) / 100 }

// paneDim is the dim that applies to one pane this frame: the configured
// amount for an unfocused pane, and none for the focused one.
//
// It doubles as the content cache's key. renderTerminal caches the string it
// built, and FocusWindow deliberately gives the pane being left the lighter
// invalidation that keeps that string, so without a key the pane you just
// stepped away from would keep serving the undimmed frame until its guest next
// wrote something. Keying the cache on the dim is the fix that no focus path
// can get around, because it is checked where the cache is used rather than
// where focus moves.
func paneDim(isFocused bool) int {
	if isFocused {
		return 0
	}
	return config.DimUnfocused
}

// dimCell fills dst with src carried toward the pane's ground and returns it,
// or returns src unchanged when there is nothing this can honestly dim.
//
// The scratch cell is the caller's because this runs once per style run rather
// than once per cell: the batching loop compares undimmed cells to decide where
// a run ends, so a run's style is built once and the blend is paid once for it.
// A run is typically a whole word or a whole line.
//
// A cell carrying the terminal's default colour is left alone unless a theme is
// set. Untheme, tuios emits colour indices and the host terminal decides what
// they look like, so there is no RGB here to carry anywhere; guessing one would
// replace the user's own palette with ours on the panes they are not looking
// at, which is a stranger result than not dimming.
func dimCell(dst, src *uv.Cell, fg, bg color.Color, t float64) *uv.Cell {
	if src == nil {
		return src
	}
	cellFg, cellBg := src.Style.Fg, src.Style.Bg
	if cellFg == nil {
		cellFg = fg
	}
	if cellBg == nil {
		cellBg = bg
	}
	if cellFg == nil && cellBg == nil {
		return src
	}
	*dst = *src
	// The fg goes to the cell's own ground where it has one, so a cell painted
	// on a block of colour dims into that block rather than into the pane
	// behind it and keeps the block readable as a block.
	if cellFg != nil {
		toward := cellBg
		if toward == nil {
			toward = bg
		}
		if toward != nil {
			dst.Style.Fg = blendColors(cellFg, toward, t)
		}
	}
	if cellBg != nil && bg != nil {
		dst.Style.Bg = blendColors(cellBg, bg, t)
	}
	return dst
}

// dimGround is the pair a dimmed cell is carried toward: the pane's own
// background, and the foreground standing in for a cell that named none. Both
// are nil when no theme is set, which is what leaves those cells alone.
func dimGround() (fg, bg color.Color) {
	if theme.CurrentThemeID() == "" {
		return nil, nil
	}
	return theme.TerminalFg(), theme.TerminalBg()
}
