package app

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// renderScrollbarLayer creates a 1-column layer overlaying the right border
// with a scrollbar indicator. Hidden during window manipulation.
func renderScrollbarLayer(window *terminal.Window, _ color.Color, zIndex int) *lipgloss.Layer {
	if window.IsBeingManipulated {
		return nil
	}

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

	// Only render the thumb portion — let the original border show through
	// for track rows so the theme border color is preserved.
	thumbFg := "\x1b[38;2;120;120;140m"
	reset := "\x1b[0m"

	var sb strings.Builder
	for i := range thumbHeight {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(thumbFg + "┃" + reset)
	}

	borderOff := window.BorderOffset()
	x := window.X + window.Width - 1
	y := window.Y + borderOff + thumbPos

	return lipgloss.NewLayer(sb.String()).
		X(x).Y(y).Z(zIndex).
		ID(window.ID + "-sb")
}
