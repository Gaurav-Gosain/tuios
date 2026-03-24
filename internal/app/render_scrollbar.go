package app

import (
	"fmt"
	"image/color"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// applyScrollbarToBorder modifies the right border characters of a bordered
// window to show a scrollbar. The thumb region uses a brighter/thicker border
// character. This integrates the scrollbar into the existing border rendering
// without needing a separate overlay layer.
func applyScrollbarToBorder(boxContent string, window *terminal.Window, borderColor color.Color) string {
	scrollbackLen := window.Terminal.ScrollbackLen()
	if scrollbackLen <= 0 {
		return boxContent
	}

	contentH := window.ContentHeight()
	if contentH <= 2 {
		return boxContent
	}

	totalLines := scrollbackLen + contentH
	thumbHeight := max((contentH*contentH+totalLines-1)/totalLines, 1)
	if thumbHeight >= contentH {
		return boxContent // Scrollbar would fill entire height
	}

	// Scroll offset from copy mode
	scrollOffset := 0
	if window.CopyMode != nil && window.CopyMode.Active {
		scrollOffset = window.CopyMode.ScrollOffset
	}

	scrollRange := contentH - thumbHeight
	// scrollOffset=0 → thumb at bottom, scrollOffset=max → thumb at top
	thumbPos := scrollRange
	if scrollOffset > 0 && scrollbackLen > 0 {
		thumbPos = scrollRange - (scrollOffset * scrollRange / scrollbackLen)
		thumbPos = max(min(thumbPos, scrollRange), 0)
	}

	// Build color strings for the scrollbar
	r, g, b, _ := borderColor.RGBA()
	cr, cg, cb := r>>8, g>>8, b>>8
	thumbFg := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", min(cr*3/2, 255), min(cg*3/2, 255), min(cb*3/2, 255))
	reset := "\x1b[0m"

	// Split content into lines. The bordered content has:
	// line 0: top border
	// lines 1..N: content lines with │ on left and right
	// line N+1: bottom border
	lines := strings.Split(boxContent, "\n")

	// Content lines start at index 1 (after top border) and end before bottom border
	for i := 1; i < len(lines)-1 && i-1 < contentH; i++ {
		contentIdx := i - 1 // 0-based content row index

		isThumb := contentIdx >= thumbPos && contentIdx < thumbPos+thumbHeight

		if isThumb {
			// Replace the last │ on this line with a bright ┃
			line := lines[i]
			lastBorder := strings.LastIndex(line, "│")
			if lastBorder >= 0 {
				// Replace │ with colored ┃
				lines[i] = line[:lastBorder] + thumbFg + "┃" + reset + line[lastBorder+len("│"):]
			}
		}
	}

	return strings.Join(lines, "\n")
}
