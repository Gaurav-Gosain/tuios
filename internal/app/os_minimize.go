package app

import (
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
				sl.EnsureFocusedVisible(m.ScrollingViewWidth())
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
	m.settleSizes(func() { m.toggleZoom() })
}

// toggleZoom is ToggleZoom with the announcements already held.
func (m *OS) toggleZoom() {
	fw := m.GetFocusedWindow()
	if fw == nil {
		return
	}

	// Zooming is structural, the same way switching tiling mode is: the
	// rectangle it lands on is final, not a step on the way to a size the user
	// is still choosing. A resize recorded before the zoom and drained after it
	// was replayed over the zoomed rectangle, so the pane shrank back to its
	// tile a tick later with the rest of the region left blank, and the guest
	// took a second announcement for a size it never had.
	m.requireRealLayout()

	// Zoom sets the pane's rectangle directly, and a snap still in flight owns
	// that rectangle: zooming while the scrolling strip was mid-slide put the
	// pane back in its column one tick later, with the emulator still at the
	// zoomed size. Retiring it also keeps the pre-zoom rectangle honest, since it
	// is read off the window a line below.
	m.CancelSnapAnimation(fw)

	if fw.Zoomed {
		// Restore from zoom
		fw.Zoomed = false
		fw.X = fw.PreZoomX
		fw.Y = fw.PreZoomY
		fw.Width = fw.PreZoomWidth
		fw.Height = fw.PreZoomHeight
		fw.InvalidateCache()
		// Route the resize through the shared path so a daemon-hosted pane is told
		// its new size too; resizing the local emulator alone leaves the app
		// unreflowed at the old size.
		fw.Resize(fw.Width, fw.Height)
		m.FlushPTYBuffersAfterResize()
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
		// Zoom fills the content region beside a reserved sidebar band: the
		// sidebar layer composes above the windows, so a zoom into the full
		// screen width would simply lose its left or right columns under it.
		leftMargin := m.GetLeftMargin()
		contentWidth := m.GetContentWidth()
		zoomWidth := contentWidth
		// If ZoomMaxWidth is set, cap width and center horizontally
		if config.ZoomMaxWidth > 0 && config.ZoomMaxWidth < contentWidth {
			zoomWidth = config.ZoomMaxWidth
		}
		fw.X = leftMargin + (contentWidth-zoomWidth)/2
		fw.Y = topMargin
		fw.Width = zoomWidth
		fw.Height = m.GetRenderHeight() - topMargin - bottomMargin
		fw.InvalidateCache()
		// Route the resize through the shared path so a daemon-hosted pane is told
		// its new size too; resizing the local emulator alone leaves the app
		// unreflowed at the old size.
		fw.Resize(fw.Width, fw.Height)
		m.FlushPTYBuffersAfterResize()
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
