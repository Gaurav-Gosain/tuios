package input

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// A wheel click is a viewport gesture, not a cursor motion.
//
// These helpers move CopyMode.ScrollOffset directly, the way Shift+Up/Down
// already did. The wheel used to drive the k/j motions instead, and those keep
// the cursor near the middle of the pane and only scroll once it gets there:
// deliberate for keyboard navigation, and wrong for a wheel. With the cursor
// down at the shell prompt, where it lands after the previous gesture scrolled
// back to the bottom or after a click, every iteration only decremented CursorY
// and the view did not move at all. On a 40-row pane that is around twenty dead
// iterations, several whole wheel clicks of nothing happening before the first
// line moved. moveUp/moveDown are untouched, so k and j still behave as before.
//
// The cursor follows the text under it rather than the screen row it sits on:
// CursorY is viewport-relative and absolute position is
// scrollbackLen-ScrollOffset+CursorY, so a scroll that did not move the cursor
// would silently slide it onto different content. Following keeps a v pressed
// after a scroll selecting from the line the user was looking at, and keeps an
// in-progress visual selection anchored to its text. It is clamped to the
// visible rows on every step, so the cursor can never end up outside the region
// being drawn: once the line it was on scrolls past an edge, the cursor rides
// that edge.

// scrollCopyModeUp scrolls the pane's viewport back by one wheel step.
func scrollCopyModeUp(win *terminal.Window) { scrollCopyModeUpBy(win, scrollStep()) }

// scrollCopyModeDown scrolls the pane's viewport forward by one wheel step.
func scrollCopyModeDown(win *terminal.Window) { scrollCopyModeDownBy(win, scrollStep()) }

// scrollCopyModeUpBy scrolls the viewport back by lines, stopping at the oldest
// line in the scrollback.
func scrollCopyModeUpBy(win *terminal.Window, lines int) {
	cm := win.CopyMode
	if cm == nil || !cm.Active {
		return
	}
	limit := win.ScrollbackLen()
	moved := min(cm.ScrollOffset+lines, limit) - cm.ScrollOffset
	if moved <= 0 {
		return
	}
	cm.ScrollOffset += moved
	win.ScrollbackOffset = cm.ScrollOffset
	setCopyCursorRow(cm, win, cm.CursorY+moved)
	win.InvalidateCache()
}

// scrollCopyModeDownBy scrolls the viewport forward by lines, stopping at the
// live screen.
func scrollCopyModeDownBy(win *terminal.Window, lines int) {
	cm := win.CopyMode
	if cm == nil || !cm.Active {
		return
	}
	moved := cm.ScrollOffset - max(cm.ScrollOffset-lines, 0)
	if moved <= 0 {
		return
	}
	cm.ScrollOffset -= moved
	win.ScrollbackOffset = cm.ScrollOffset
	setCopyCursorRow(cm, win, cm.CursorY-moved)
	win.InvalidateCache()
}

// setCopyCursorRow puts the copy-mode cursor on a viewport row, clamped to the
// rows that are actually drawn.
func setCopyCursorRow(cm *terminal.CopyMode, win *terminal.Window, row int) {
	cm.CursorY = max(min(row, win.ContentHeight()-1), 0)
}

// scrollStep is the number of lines one wheel click moves, from
// appearance.scroll_lines, floored at one so a misconfigured zero cannot make
// the wheel inert.
func scrollStep() int {
	return max(config.ScrollLines, 1)
}

// leaveCopyModeAtBottom ends an implicit copy-mode session that has scrolled
// back to the live screen.
//
// Reaching the bottom and being done are the same event: a wheel gesture that
// walks the view back down to live output has nothing left to render from
// scrollback, so the session that existed only to render it goes away. It goes
// quietly, because there was nothing to announce on the way in either.
//
// An explicit session is left alone. The wheel used to throw the user out of
// copy mode they had asked for, once the cursor happened to reach the last row,
// which is a surprise in the other direction; q and Esc still exit, and both
// already return the pane to the bottom.
//
// A selection in progress holds the session open: dragging past the bottom edge
// auto-scrolls, and exiting at zero would drop the drag mid-gesture.
func leaveCopyModeAtBottom(win *terminal.Window) {
	cm := win.CopyMode
	if cm == nil || !cm.Active || !cm.Implicit {
		return
	}
	if cm.ScrollOffset != 0 || cm.State != terminal.CopyModeNormal {
		return
	}
	win.ExitCopyMode()
}
