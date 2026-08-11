package app

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// scrollbarViewOffset returns how far back into scrollback the pane is looking,
// 0 at the live tail.
func scrollbarViewOffset(window *terminal.Window) int {
	if !window.InCopyMode() {
		return 0
	}
	return max(window.CopyMode.ScrollOffset, 0)
}

// scrollbarThumbHeight sizes the thumb to the viewport's share of the whole
// buffer, leaving at least one cell of travel so the bar always reads as a
// position rather than a filled track.
func scrollbarThumbHeight(contentH, scrollbackLen int) int {
	total := scrollbackLen + contentH
	h := (contentH*contentH + total - 1) / total
	return max(min(h, contentH-1), 1)
}

// windowNeedsScrollbar reports whether window should show a scrollbar thumb.
// It is the single source of truth shared by every render path (compositor
// cached, sync-hold, redraw, and the fullscreen fast path) so they never
// disagree about whether the thumb is present. It mirrors the eligibility in
// renderScrollbarLayer minus the transient IsBeingManipulated check.
//
// The thumb is a position readout, so it exists only while there is a position
// to read: a bar pinned to the bottom at the live tail is chrome answering a
// question nobody asked, and it is the only thing that used to drop a lone
// scrolled-to-tail pane off the fullscreen fast path.
func windowNeedsScrollbar(window *terminal.Window) bool {
	if config.HideScrollbar {
		return false
	}
	if window.Terminal == nil || window.IsAltScreen() {
		return false
	}
	if scrollbarViewOffset(window) <= 0 {
		return false
	}
	if window.ScrollbackLenSync() <= 0 {
		return false
	}
	return window.ContentHeight() > 2
}

// scrollbarColumn returns the screen column the thumb occupies: the pane's last
// content column, one in from its right border when it has one. A borderless
// pane under shared borders has BorderOffset 0, so the column is its own
// rightmost cell, one in from the separator overlay that lives in the gap
// between rectangles. The bar was never the border's business, which is why one
// formula covers bordered, borderless, hidden-border and zoomed panes alike.
func scrollbarColumn(window *terminal.Window) int {
	return window.X + window.Width - 1 - window.BorderOffset()
}

// ScrollbarHit reports the column a scrollbar drag may be grabbed at, and
// whether one is on screen to grab. Input reads it so a press lands on cells
// the renderer actually painted; at the live tail the column is ordinary
// content and belongs to the guest.
func ScrollbarHit(window *terminal.Window) (int, bool) {
	if window == nil || !windowNeedsScrollbar(window) {
		return 0, false
	}
	return scrollbarColumn(window), true
}

// renderScrollbarLayer creates a 1-column layer floating the thumb over the
// pane's last content column. rightClip is the first column of the sidebar
// band; a pane mid-drag may straddle it, and the thumb is composed above the
// band's own layer.
func renderScrollbarLayer(window *terminal.Window, rightClip, zIndex int) *lipgloss.Layer {
	if window.IsBeingManipulated || !windowNeedsScrollbar(window) {
		return nil
	}

	x := scrollbarColumn(window)
	if x < 0 || x >= rightClip {
		return nil
	}

	scrollbackLen := window.ScrollbackLenSync()
	contentH := window.ContentHeight()
	thumbHeight := scrollbarThumbHeight(contentH, scrollbackLen)

	// Offset 0 pins the thumb to the bottom, a full offset to the top.
	scrollRange := contentH - thumbHeight
	thumbPos := scrollRange - (scrollbarViewOffset(window) * scrollRange / scrollbackLen)
	thumbPos = max(min(thumbPos, scrollRange), 0)

	thumb := strings.TrimSuffix(strings.Repeat(config.GetScrollbarThumbChar()+"\n", thumbHeight), "\n")

	// The quiet grey the unfocused frames already use, for every pane. The bar
	// reports scroll position, not focus, so deriving it from the live border
	// colour made the frame itself mutate as panes gained focus.
	return lipgloss.NewLayer(lipgloss.NewStyle().Foreground(theme.BorderUnfocused()).Render(thumb)).
		X(x).Y(window.Y + window.BorderOffset() + thumbPos).Z(zIndex).
		ID(window.ID + "-sb")
}
