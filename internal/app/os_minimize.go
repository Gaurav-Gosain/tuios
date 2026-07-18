package app

import (
	"fmt"
	"os"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// MinimizeWindow minimizes the window at the specified index.
func (m *OS) MinimizeWindow(i int) {
	if i >= 0 && i < len(m.Windows) && !m.Windows[i].Minimized && !m.Windows[i].Minimizing {
		// Get pointer to the actual window (not a copy)
		window := m.Windows[i]

		// Store current position before minimizing
		window.PreMinimizeX = window.X
		window.PreMinimizeY = window.Y
		window.PreMinimizeWidth = window.Width
		window.PreMinimizeHeight = window.Height

		// Immediately minimize without animation
		now := time.Now()
		window.Minimized = true
		window.Minimizing = false
		window.MinimizeOrder = now.UnixNano() // Track order for dock sorting

		// Set highlight timestamp for dock tab
		window.MinimizeHighlightUntil = now.Add(1 * time.Second)

		// DEBUG: Log minimize action
		if os.Getenv("TUIOS_DEBUG_INTERNAL") == "1" {
			if f, err := os.OpenFile("/tmp/tuios-minimize-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600); err == nil {
				_, _ = fmt.Fprintf(f, "[MINIMIZE] Window index=%d, ID=%s, CustomName=%s, Highlight set until %s\n",
					i, window.ID, window.CustomName, window.MinimizeHighlightUntil.Format("15:04:05.000"))
				_ = f.Close()
			}
		}

		// Change focus to next visible window
		if i == m.FocusedWindow {
			m.FocusNextVisibleWindow()
		}

		// Retile remaining windows if in tiling mode
		if m.AutoTiling {
			if m.UseScrollingLayout {
				// Remove from scrolling layout and retile
				intID := m.getWindowIntID(window.ID)
				sl := m.GetOrCreateScrollingLayout()
				sl.RemoveWindow(intID)
				sl.EnsureFocusedVisible(m.GetRenderWidth())
				m.scrollingSetPositions()
			} else if m.UseBSPLayout {
				// Remove from the BSP tree and reflow the remaining panes,
				// mirroring the close path (DeleteWindow). Using the
				// master-stack tiler here would ignore the tree and leave a
				// stale window ID behind, discarding custom split ratios.
				m.RemoveWindowFromBSPTree(window)
				m.ApplyBSPLayout()
			} else {
				m.TileRemainingWindows(i)
			}
		}
	}
}

// RestoreWindow restores a minimized window at the specified index.
func (m *OS) RestoreWindow(i int) {
	if i >= 0 && i < len(m.Windows) && m.Windows[i].Minimized {
		window := m.Windows[i]

		// In tiling mode, skip animation and let TileAllWindows() handle positioning
		// This prevents incorrect tiling calculations when restoring multiple windows
		if m.AutoTiling {
			window.Minimized = false

			if m.UseScrollingLayout {
				// Re-add to scrolling layout
				intID := m.getWindowIntID(window.ID)
				sl := m.GetOrCreateScrollingLayout()
				if !sl.HasWindow(intID) {
					sl.AddColumn(intID)
				}
			}

			// Bring the window to front and focus it
			m.FocusWindow(i)
			m.TileAllWindows()
			return
		}

		// Non-tiling mode: create smooth animation to PreMinimize position
		// Create and start animation
		anim := m.CreateRestoreAnimation(i)
		if anim != nil {
			// Set window to animation start position (dock position) to avoid flashing
			window.X = anim.StartX
			window.Y = anim.StartY
			window.Width = anim.StartWidth
			window.Height = anim.StartHeight

			m.Animations = append(m.Animations, anim)
		}

		// Mark as not minimized after setting position so it shows during animation
		window.Minimized = false

		// Bring the window to front and focus it
		m.FocusWindow(i)
		// Enter window management mode to interact with the restored window
		m.Mode = WindowManagementMode
	}
}

// ToggleZoom toggles the focused window between zoomed (fullscreen) and normal state.
// When zoomed, the window fills the entire viewport (minus dock). When unzoomed, it
// returns to its previous size and position. Other windows are hidden while zoomed.
func (m *OS) ToggleZoom() {
	fw := m.GetFocusedWindow()
	if fw == nil {
		return
	}

	if fw.Zoomed {
		// Restore from zoom
		fw.Zoomed = false
		fw.X = fw.PreZoomX
		fw.Y = fw.PreZoomY
		fw.Width = fw.PreZoomWidth
		fw.Height = fw.PreZoomHeight
		fw.InvalidateCache()
		// Resize terminal to match restored dimensions
		termW := fw.ContentWidth()
		termH := fw.ContentHeight()
		if fw.Terminal != nil {
			fw.LockIO()
			fw.Terminal.Resize(termW, termH)
			fw.UnlockIO()
		}
		if fw.Pty != nil {
			_ = fw.Pty.Resize(termW, termH)
		}
		// If tiling, retile all
		if m.AutoTiling {
			m.TileAllWindows()
		}
		m.MarkAllDirty()
	} else {
		// Save current position and zoom to fullscreen
		fw.PreZoomX = fw.X
		fw.PreZoomY = fw.Y
		fw.PreZoomWidth = fw.Width
		fw.PreZoomHeight = fw.Height
		fw.Zoomed = true

		// Calculate zoom dimensions, respecting the dockbar's reserved space.
		topMargin := 0
		if config.DockbarPosition == "top" {
			topMargin = config.DockHeight
		}
		bottomMargin := 0
		if config.DockbarPosition == "bottom" {
			bottomMargin = config.DockHeight
		}
		screenWidth := m.GetRenderWidth()
		zoomWidth := screenWidth
		// If ZoomMaxWidth is set, cap width and center horizontally
		if config.ZoomMaxWidth > 0 && config.ZoomMaxWidth < screenWidth {
			zoomWidth = config.ZoomMaxWidth
		}
		fw.X = (screenWidth - zoomWidth) / 2
		fw.Y = topMargin
		fw.Width = zoomWidth
		fw.Height = m.GetRenderHeight() - topMargin - bottomMargin
		fw.InvalidateCache()
		// Resize terminal to match zoomed dimensions
		termW := fw.ContentWidth()
		termH := fw.ContentHeight()
		if fw.Terminal != nil {
			fw.LockIO()
			fw.Terminal.Resize(termW, termH)
			fw.UnlockIO()
		}
		if fw.Pty != nil {
			_ = fw.Pty.Resize(termW, termH)
		}
		m.MarkAllDirty()
	}
}

// RestoreMinimizedByIndex restores a minimized window by its minimized index.
func (m *OS) RestoreMinimizedByIndex(index int) {
	// Find the nth minimized window in current workspace
	minimizedCount := 0
	for i, window := range m.Windows {
		if window.Workspace == m.CurrentWorkspace && window.Minimized {
			if minimizedCount == index {
				m.RestoreWindow(i)
				return
			}
			minimizedCount++
		}
	}
}

// FocusNextVisibleWindow focuses the next visible window in the current workspace.
func (m *OS) FocusNextVisibleWindow() {
	// Find the next non-minimized and non-minimizing window to focus in current workspace
	// Start from the beginning to find any visible window

	// First pass: find any visible window in current workspace
	for i := range len(m.Windows) {
		if m.Windows[i].Workspace == m.CurrentWorkspace && !m.Windows[i].Minimized && !m.Windows[i].Minimizing {
			m.FocusWindow(i)
			return
		}
	}

	// No visible windows in workspace, set focus to -1
	m.FocusedWindow = -1
}

// HasMinimizedWindows returns true if there are any minimized windows.
func (m *OS) HasMinimizedWindows() bool {
	for _, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && w.Minimized {
			return true
		}
	}
	return false
}
