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
		return boxContent
	}

	// Scroll offset from copy mode
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

	// Build thumb color — brighter version of the border color
	r, g, b, _ := borderColor.RGBA()
	cr, cg, cb := r>>8, g>>8, b>>8
	thumbFg := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", min(cr*3/2, 255), min(cg*3/2, 255), min(cb*3/2, 255))
	reset := "\x1b[0m"

	lines := strings.Split(boxContent, "\n")

	// Content lines: index 1 to len-2 (skip top border at 0 and bottom border at last)
	for i := 1; i < len(lines)-1 && i-1 < contentH; i++ {
		contentIdx := i - 1
		isThumb := contentIdx >= thumbPos && contentIdx < thumbPos+thumbHeight
		if !isThumb {
			continue
		}

		// Find the LAST occurrence of the │ rune in this line.
		// It may be wrapped in ANSI escape codes.
		// Scan backwards through runes to find it.
		runes := []rune(lines[i])
		lastIdx := -1
		for j := len(runes) - 1; j >= 0; j-- {
			if runes[j] == '│' {
				lastIdx = j
				break
			}
		}

		if lastIdx >= 0 {
			// Replace │ with ┃ wrapped in thumb color
			// We need to inject the color around it. Since the │ may already
			// be wrapped in a color sequence from lipgloss, we replace the
			// rune and add our own color before it + reset after.
			// Build new line: everything before the │ + thumbFg + ┃ + reset + everything after
			before := string(runes[:lastIdx])
			after := string(runes[lastIdx+1:])

			// Strip any trailing ANSI reset that was after the old │
			// to avoid double resets
			lines[i] = before + thumbFg + "┃" + reset + after
		}
	}

	return strings.Join(lines, "\n")
}
