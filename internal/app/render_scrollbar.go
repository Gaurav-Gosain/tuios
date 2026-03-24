package app

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// renderScrollbarLayer creates a single-column lipgloss Layer that overlays
// the right border of a window with a scrollbar. The thumb is a bright ┃,
// the track is a dim │. Positioned exactly on the right border column.
func renderScrollbarLayer(window *terminal.Window, borderColor color.Color, zIndex int) *lipgloss.Layer {
	scrollbackLen := window.Terminal.ScrollbackLen()
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
		return nil // No scrollbar needed
	}

	scrollOffset := 0
	if window.CopyMode != nil && window.CopyMode.Active {
		scrollOffset = window.CopyMode.ScrollOffset
	}

	scrollRange := contentH - thumbHeight
	thumbPos := scrollRange // default at bottom
	if scrollOffset > 0 && scrollbackLen > 0 {
		thumbPos = scrollRange - (scrollOffset * scrollRange / scrollbackLen)
		thumbPos = max(min(thumbPos, scrollRange), 0)
	}

	// Colors: bright for thumb, dim for track, derived from border color
	r, g, b, _ := borderColor.RGBA()
	cr, cg, cb := r>>8, g>>8, b>>8
	thumbFg := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", min(cr*2, 255), min(cg*2, 255), min(cb*2, 255))
	trackFg := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", cr, cg, cb)
	reset := "\x1b[0m"

	var sb strings.Builder
	for y := range contentH {
		if y > 0 {
			sb.WriteByte('\n')
		}
		if y >= thumbPos && y < thumbPos+thumbHeight {
			sb.WriteString(thumbFg + "┃" + reset)
		} else {
			sb.WriteString(trackFg + "│" + reset)
		}
	}

	// Position on the right border column, skipping top border row
	borderOff := window.BorderOffset()
	x := window.X + window.Width - 1
	y := window.Y + borderOff

	return lipgloss.NewLayer(sb.String()).
		X(x).Y(y).Z(zIndex).
		ID(window.ID + "-sb")
}
