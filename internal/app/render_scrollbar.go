package app

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// windowNeedsScrollbar reports whether window should show a scrollbar thumb.
// It is the single source of truth shared by every render path (compositor
// cached, sync-hold, redraw, and the fullscreen fast path) so they never
// disagree about whether the thumb is present. It mirrors the eligibility in
// renderScrollbarLayer minus the transient IsBeingManipulated check.
func windowNeedsScrollbar(window *terminal.Window) bool {
	if config.HideScrollbar || config.BorderStyle == "hidden" {
		return false
	}
	if window.Terminal == nil || window.IsAltScreen() {
		return false
	}
	// A borderless pane has no scrollbar. This is deliberate and it has no
	// exceptions: the thumb is drawn on the pane's right border cell, and a
	// borderless pane has none, so the only column available is the rightmost
	// column of the guest's own output. Shared borders is therefore a
	// scrollbar-free mode by choice, zoomed panes included - a zoomed pane is
	// still full-rect and still borderless, so there is no more room for a thumb
	// there than in a two-pane split. Putting one on the separator overlay was
	// considered and rejected: that line lies between two rectangles and belongs
	// to neither, and shared borders is a mode chosen for a quieter screen.
	if rendersBorderless(window) {
		return false
	}
	scrollbackLen := window.ScrollbackLenSync()
	if scrollbackLen <= 0 {
		return false
	}
	contentH := window.ContentHeight()
	if contentH <= 2 {
		return false
	}
	totalLines := scrollbackLen + contentH
	thumbHeight := max((contentH*contentH+totalLines-1)/totalLines, 1)
	return thumbHeight < contentH
}

// renderScrollbarLayer creates a 1-column layer overlaying the right border
// with a scrollbar indicator. Hidden during window manipulation, when the
// scrollbar is disabled via config, when the border style is "hidden", or when
// the pane is borderless (no border cell to overlay the thumb on).
func renderScrollbarLayer(window *terminal.Window, borderColor color.Color, zIndex int) *lipgloss.Layer {
	if window.IsBeingManipulated {
		return nil
	}
	if config.HideScrollbar || config.BorderStyle == "hidden" {
		return nil
	}

	// Repeated from windowNeedsScrollbar rather than left to the callers. Every
	// call site gates on windowNeedsScrollbar today, but this function is what
	// actually writes the cells, and the cells it would write on a borderless
	// pane are the guest's output. A guard that only lives in the caller is one
	// forgotten call away from painting over a user's terminal.
	if rendersBorderless(window) {
		return nil
	}

	// Hide scrollbar when the window is in alt screen (nvim, btop, etc.).
	// Alt screen apps manage their own viewport and scrollback is not used.
	if window.IsAltScreen() {
		return nil
	}

	scrollbackLen := window.ScrollbackLenSync()
	if scrollbackLen <= 0 {
		return nil
	}

	contentH := window.ContentHeight()
	if contentH <= 2 {
		return nil
	}

	totalLines := scrollbackLen + contentH
	thumbHeight := max((contentH*contentH+totalLines-1)/totalLines, 1)
	if thumbHeight >= contentH {
		return nil
	}

	scrollOffset := 0
	if window.CopyMode != nil && window.CopyMode.Active {
		scrollOffset = window.CopyMode.ScrollOffset
	}

	scrollRange := contentH - thumbHeight
	thumbPos := scrollRange
	if scrollOffset > 0 && scrollbackLen > 0 {
		thumbPos = scrollRange - (scrollOffset * scrollRange / scrollbackLen)
		thumbPos = max(min(thumbPos, scrollRange), 0)
	}

	// Use a darker version of the border color for the thumb
	r, g, b, _ := borderColor.RGBA()
	cr, cg, cb := r>>8, g>>8, b>>8
	// Darken the border color by 40% for visible contrast against the
	// border line. Use a thumb character that matches the active border
	// weight so the scrollbar blends in visually.
	thumbFg := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", cr*60/100, cg*60/100, cb*60/100)
	reset := "\x1b[0m"
	thumbChar := config.GetScrollbarThumbChar()

	var sb strings.Builder
	for i := range thumbHeight {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(thumbFg + thumbChar + reset)
	}

	borderOff := window.BorderOffset()
	x := window.X + window.Width - 1
	y := window.Y + borderOff + thumbPos

	return lipgloss.NewLayer(sb.String()).
		X(x).Y(y).Z(zIndex).
		ID(window.ID + "-sb")
}
