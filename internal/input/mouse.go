// Package input implements mouse event handling for TUIOS.
package input

import (
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
)

// isInTerminalContent checks if coordinates are within the terminal's content area.
// The content area excludes the window borders (1 cell on each side, 0 for tiled).
func isInTerminalContent(x, y int, win *terminal.Window) bool {
	return x >= 0 && y >= 0 && x < win.ContentWidth() && y < win.ContentHeight()
}

// sendMouseToWindow forwards a mouse event to a window's terminal.
// In daemon mode, the event is encoded as an escape sequence and written via PTY.
// In local mode, the event is sent directly to the emulator.
func sendMouseToWindow(win *terminal.Window, event uv.MouseEvent) {
	if win.Terminal == nil {
		return
	}
	// SendMouse/EncodeMouseEvent read the emulator mode map, which the emulator
	// guards internally against the PTY reader goroutine.
	if win.DaemonMode {
		seq := win.Terminal.EncodeMouseEvent(event)
		if seq != "" {
			_ = win.SendInput([]byte(seq))
		}
	} else {
		win.Terminal.SendMouse(event)
	}
}

// Hit testing helpers

// findClickedWindow finds the topmost window at the given coordinates
func findClickedWindow(x, y int, o *app.OS) int {
	// Find the topmost window (highest Z) that contains the click point
	topWindow := -1
	topZ := -1

	for i, window := range o.Windows {
		// Skip windows not in current workspace
		if window.Workspace != o.CurrentWorkspace {
			continue
		}
		// Skip minimized windows
		if window.Minimized {
			continue
		}
		// Check if click is within window bounds
		if x >= window.X && x < window.X+window.Width &&
			y >= window.Y && y < window.Y+window.Height {
			// This window contains the click - check if it's the topmost so far
			if window.Z > topZ {
				topZ = window.Z
				topWindow = i
			}
		}
	}

	return topWindow
}

// abs returns the absolute value of an integer
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// scrollToPosition scrolls a window's copy mode to the position indicated
// by the mouse Y coordinate on the scrollbar (right border).
func scrollToPosition(win *terminal.Window, mouseY int) {
	if win.Terminal == nil {
		return
	}
	win.RLockIO()
	scrollbackLen := win.Terminal.ScrollbackLen()
	win.RUnlockIO()
	if scrollbackLen <= 0 {
		return
	}

	// Dragging the scrollbar is a scroll gesture like the wheel, so it enters
	// copy mode the same silent way: the scrollback has to be rendered by
	// something, but the user did not ask to be put in a mode.
	if !win.InCopyMode() {
		win.EnterCopyModeImplicit()
	}
	if win.CopyMode == nil {
		return
	}

	borderOff := win.BorderOffset()
	contentH := win.ContentHeight()
	relY := mouseY - win.Y - borderOff
	relY = max(min(relY, contentH-1), 0)

	// relY=0 → top (max scroll), relY=contentH-1 → bottom (0 scroll)
	scrollOffset := scrollbackLen - (relY * scrollbackLen / max(contentH-1, 1))
	scrollOffset = max(min(scrollOffset, scrollbackLen), 0)

	win.CopyMode.ScrollOffset = scrollOffset
	win.ScrollbackOffset = scrollOffset // Sync for rendering
	win.InvalidateCache()

	// Dragged all the way to the bottom is the same as scrolling there with the
	// wheel: back to live output, with nothing left over.
	leaveCopyModeAtBottom(win)
}
