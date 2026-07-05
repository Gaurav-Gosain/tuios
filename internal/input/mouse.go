// Package input implements mouse event handling for TUIOS.
package input

import (
	"fmt"
	"os"

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

// findDockItemClicked finds which dock item was clicked
func findDockItemClicked(x, y int, o *app.OS) int {
	// Use shared layout calculation to ensure positions match rendering exactly
	layout := o.CalculateDockLayout()

	// DEBUG: Log dock click attempts
	if os.Getenv("TUIOS_DEBUG_INTERNAL") == "1" {
		if f, err := os.OpenFile("/tmp/tuios-dock-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			_, _ = fmt.Fprintf(f, "[DOCK CLICK] X=%d Y=%d, Height=%d, CenterStartX=%d, numItems=%d, numVisible=%d\n",
				x, y, o.Height, layout.CenterStartX, len(layout.ItemPositions), len(layout.VisibleItems))
			_ = f.Close()
		}
	}

	// Check which item was clicked using the calculated positions
	for i, itemPos := range layout.ItemPositions {
		// DEBUG: Log each item bounds
		if os.Getenv("TUIOS_DEBUG_INTERNAL") == "1" {
			if f, err := os.OpenFile("/tmp/tuios-dock-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
				_, _ = fmt.Fprintf(f, "[DOCK ITEM %d] windowIndex=%d, Clickable [%d,%d), Y=%d (checking Y==%d)\n",
					i, itemPos.WindowIndex, itemPos.StartX, itemPos.EndX, o.Height-1, y)
				_ = f.Close()
			}
		}

		// Check if click is within this dock item
		if x >= itemPos.StartX && x < itemPos.EndX && y == o.GetDockbarContentYPosition() {
			// DEBUG: Log successful match
			if os.Getenv("TUIOS_DEBUG_INTERNAL") == "1" {
				if f, err := os.OpenFile("/tmp/tuios-dock-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
					_, _ = fmt.Fprintf(f, "[DOCK MATCH] Item %d (windowIndex=%d) matched! Click X=%d in range [%d,%d)\n",
						i, itemPos.WindowIndex, x, itemPos.StartX, itemPos.EndX)
					_ = f.Close()
				}
			}
			return itemPos.WindowIndex
		}
	}

	return -1
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
	scrollbackLen := win.Terminal.ScrollbackLen()
	if scrollbackLen <= 0 {
		return
	}

	// Enter copy mode if not already
	if win.CopyMode == nil || !win.CopyMode.Active {
		win.EnterCopyMode()
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
}
