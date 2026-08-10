package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Snap snaps the window at index i to the specified position.
func (m *OS) Snap(i int, quarter SnapQuarter) *OS {
	if i < 0 || i >= len(m.Windows) {
		return m
	}

	// Create and start snap animation
	anim := m.CreateSnapAnimation(i, quarter)
	if anim != nil {
		m.Animations = append(m.Animations, anim)
	} else {
		// No animation needed (already at target), but still resize terminal if needed
		win := m.Windows[i]
		_, _, targetWidth, targetHeight := m.calculateSnapBounds(quarter)

		// Enforce minimum size
		targetWidth = max(targetWidth, config.DefaultWindowWidth)
		targetHeight = max(targetHeight, config.DefaultWindowHeight)

		// Make sure terminal is properly sized even if no animation
		if win.Width != targetWidth || win.Height != targetHeight {
			win.Resize(targetWidth, targetHeight)
		}
	}

	return m
}
func (m *OS) calculateSnapBounds(quarter SnapQuarter) (x, y, width, height int) {
	usableHeight := m.GetUsableHeight()
	renderWidth := m.GetRenderWidth()
	halfWidth := renderWidth / 2
	halfHeight := usableHeight / 2
	topMargin := m.GetTopMargin()

	switch quarter {
	case SnapLeft:
		return 0, topMargin, halfWidth, usableHeight
	case SnapRight:
		return halfWidth, topMargin, renderWidth - halfWidth, usableHeight
	case SnapTopLeft:
		return 0, topMargin, halfWidth, halfHeight
	case SnapTopRight:
		return halfWidth, topMargin, halfWidth, halfHeight
	case SnapBottomLeft:
		return 0, halfHeight + topMargin, halfWidth, usableHeight - halfHeight
	case SnapBottomRight:
		return halfWidth, halfHeight + topMargin, halfWidth, usableHeight - halfHeight
	case SnapFullScreen:
		return 0, topMargin, renderWidth, usableHeight
	case Unsnap:
		return renderWidth / 4, usableHeight/4 + topMargin, halfWidth, halfHeight
	default:
		return renderWidth / 4, usableHeight/4 + topMargin, halfWidth, halfHeight
	}
}

// ScaleWindowsToTerminal proportionally scales all windows when terminal size changes.
// This is called when restoring from daemon state to ensure windows fit the new terminal size.
// oldWidth/oldHeight are the terminal dimensions when state was saved.
// newWidth/newHeight are the current terminal dimensions.
func (m *OS) ScaleWindowsToTerminal(oldWidth, oldHeight, newWidth, newHeight int) {
	if m.AutoTiling {
		return // Tiling mode handles its own layout
	}

	if oldWidth <= 0 || oldHeight <= 0 || newWidth <= 0 || newHeight <= 0 {
		return // Invalid dimensions
	}

	oldUsableHeight := oldHeight - m.GetTopMargin()
	if config.DockbarPosition != "hidden" {
		oldUsableHeight -= 1
	}

	newUsableHeight := m.GetUsableHeight()
	newRenderWidth := m.GetRenderWidth()

	widthScale := float64(newRenderWidth) / float64(oldWidth)
	heightScale := float64(newUsableHeight) / float64(oldUsableHeight)

	m.LogInfo("[SCALE] Scaling windows: width %.2fx, height %.2fx", widthScale, heightScale)

	for _, win := range m.Windows {
		if win.Minimized {
			continue
		}

		// Scale position and size
		win.X = int(float64(win.X) * widthScale)
		win.Y = int(float64(win.Y) * heightScale)
		win.Width = int(float64(win.Width) * widthScale)
		win.Height = int(float64(win.Height) * heightScale)

		// Ensure minimum size
		if win.Width < config.DefaultWindowWidth {
			win.Width = config.DefaultWindowWidth
		}
		if win.Height < config.DefaultWindowHeight {
			win.Height = config.DefaultWindowHeight
		}

		// Ensure windows don't exceed terminal bounds
		if win.Width > newRenderWidth {
			win.Width = newRenderWidth
		}
		if win.Height > newUsableHeight {
			win.Height = newUsableHeight
		}

		// Ensure position keeps window on screen
		if win.X < 0 {
			win.X = 0
		}
		if win.Y < 0 {
			win.Y = 0
		}
		if win.X+win.Width > newRenderWidth {
			win.X = newRenderWidth - win.Width
		}
		if win.Y+win.Height > newUsableHeight {
			win.Y = newUsableHeight - win.Height
		}

		// Mark dirty and resize PTY
		win.MarkPositionDirty()
		win.Resize(win.Width, win.Height)
	}
}

// ClampWindowsToView ensures all floating windows are visible within the current terminal bounds.
// This is called when reattaching with a smaller terminal or when the terminal shrinks.
// Windows that would be off-screen are repositioned to remain visible.
func (m *OS) ClampWindowsToView() {
	if m.AutoTiling {
		return // Tiling mode handles its own layout
	}

	usableHeight := m.GetUsableHeight()
	renderWidth := m.GetRenderWidth()
	// The sidebar reserves a horizontal band the same way the dock reserves a
	// vertical one, so a floating pane is clamped against the content region
	// [leftMargin, leftMargin+contentWidth), not the full screen. Without this a
	// pane could be shoved wholly under the sidebar and become unreachable.
	leftMargin := m.GetLeftMargin()
	contentWidth := m.GetContentWidth()
	rightEdge := leftMargin + contentWidth
	topMargin := m.GetTopMargin()
	minVisibleX := 20 // Minimum visible horizontal pixels (matches mouse.go)
	minVisibleY := 3  // Minimum visible vertical rows (matches mouse.go)
	clampedCount := 0

	for _, win := range m.Windows {
		if win.Workspace != m.CurrentWorkspace || win.Minimized {
			continue
		}

		originalX, originalY := win.X, win.Y
		needsResize := false

		// Clamp window size to fit within the content region if larger
		if win.Width > contentWidth {
			win.Width = contentWidth
			needsResize = true
		}
		if win.Height > usableHeight {
			win.Height = usableHeight
			needsResize = true
		}

		// Ensure minimum size
		if win.Width < config.DefaultWindowWidth {
			win.Width = config.DefaultWindowWidth
			needsResize = true
		}
		if win.Height < config.DefaultWindowHeight {
			win.Height = config.DefaultWindowHeight
			needsResize = true
		}

		// Clamp X position: ensure at least minVisibleX pixels are visible within
		// the content region, so a pane can never hide fully behind the sidebar.
		if win.X+win.Width < leftMargin+minVisibleX {
			win.X = leftMargin + minVisibleX - win.Width
		}
		if win.X > rightEdge-minVisibleX {
			win.X = rightEdge - minVisibleX
		}

		// Clamp Y position: ensure at least minVisibleY rows visible, and can't go behind dock
		if win.Y < topMargin {
			win.Y = topMargin
		}
		maxY := topMargin + usableHeight - minVisibleY
		if win.Y > maxY {
			win.Y = maxY
		}

		// If position changed, mark as dirty and log
		if win.X != originalX || win.Y != originalY || needsResize {
			win.MarkPositionDirty()
			if needsResize {
				win.Resize(win.Width, win.Height)
			}
			clampedCount++
		}
	}

	if clampedCount > 0 {
		m.LogInfo("[CLAMP] Repositioned %d windows to fit terminal bounds (%dx%d)", clampedCount, renderWidth, m.GetRenderHeight())
		m.SyncStateToDaemon()
	}
}

// GetTopMargin returns the margin at the top (reserved space for the dockbar
// when positioned at "top").
func (m *OS) GetTopMargin() int {
	if config.DockbarPosition == "top" {
		return config.DockHeight
	}

	return 0
}

// GetDockbarContentYPosition returns the Y position of the dockbar
func (m *OS) GetDockbarContentYPosition() int {
	if config.DockbarPosition == "top" {
		return 0
	}

	return m.Height - 1
}

// GetTimeYPosition returns the Y position of the time display
func (m *OS) GetTimeYPosition() int {
	if config.DockbarPosition == "top" {
		return m.Height - 1
	}

	return 0
}

// GetUsableHeight returns the usable height excluding the dock. Auto-hide
// mode keeps the reservation so tiled windows have a stable layout  - the dock
// only hides when a specific window (zoom/float) explicitly expands into its
// rows.
func (m *OS) GetUsableHeight() int {
	if config.DockbarPosition == "hidden" {
		return m.GetRenderHeight()
	}
	return m.GetRenderHeight() - config.DockHeight
}

// GetRenderWidth returns the width to use for rendering.
// In multi-client mode, this is the minimum of the terminal width and
// the effective session width (min of all connected clients).
func (m *OS) GetRenderWidth() int {
	// If terminal size not yet known, use effective size if available
	if m.Width == 0 {
		if m.EffectiveWidth > 0 {
			return m.EffectiveWidth
		}
		return 0
	}
	// Use minimum of terminal and effective size
	if m.EffectiveWidth > 0 && m.EffectiveWidth < m.Width {
		return m.EffectiveWidth
	}
	return m.Width
}

// GetSidebarWidth is the single function that folds the configured sidebar
// width together with the narrow-screen breakpoints, exactly as overlay.FitWidth
// folds a panel's preferred width with the screen width. It returns the number
// of columns the sidebar actually reserves, or 0 when the sidebar is off, hidden,
// or the screen is too narrow to carry it.
//
// Breakpoints, measured against the render width:
//
//	>= 90 cols: full sidebar at config.SidebarWidth
//	60-89 cols: narrow rail (glyph + short name)
//	40-59 cols: glyph-only rail
//	< 40 cols:  auto-hidden regardless of config
//
// After picking a variant it enforces a pane floor: the content area never drops
// below SidebarMinPaneFloor columns, so the sidebar can never squeeze the panes
// to nothing. If the floor would be violated it steps down to a narrower variant
// first, then hides.
func (m *OS) GetSidebarWidth() int {
	if !config.SidebarEnabled || config.SidebarPosition == "hidden" {
		return 0
	}
	rw := m.GetRenderWidth()
	if rw <= 0 {
		return 0
	}

	var w int
	switch {
	case rw < config.SidebarBreakpointGlyph:
		return 0
	case rw < config.SidebarBreakpointNarrow:
		w = config.SidebarGlyphWidth
	case rw < config.SidebarBreakpointFull:
		w = config.SidebarNarrowWidth
	default:
		w = max(config.SidebarWidth, config.SidebarGlyphWidth)
	}

	// Enforce the pane floor by stepping down to a narrower variant rather than
	// starving the content area.
	if rw-w < config.SidebarMinPaneFloor {
		switch {
		case rw-config.SidebarNarrowWidth >= config.SidebarMinPaneFloor:
			w = config.SidebarNarrowWidth
		case rw-config.SidebarGlyphWidth >= config.SidebarMinPaneFloor:
			w = config.SidebarGlyphWidth
		default:
			return 0
		}
	}
	return w
}

// GetLeftMargin returns the reserved columns on the left: the sidebar width when
// it is enabled and positioned on the left, else 0.
func (m *OS) GetLeftMargin() int {
	if config.SidebarPosition == "left" {
		return m.GetSidebarWidth()
	}
	return 0
}

// GetRightMargin returns the reserved columns on the right: the sidebar width
// when it is enabled and positioned on the right, else 0.
func (m *OS) GetRightMargin() int {
	if config.SidebarPosition == "right" {
		return m.GetSidebarWidth()
	}
	return 0
}

// GetContentWidth returns the width available to panes: the render width less the
// left and right reserved margins. This is what tiling partitions and what the
// pane floor is enforced against.
func (m *OS) GetContentWidth() int {
	return m.GetRenderWidth() - m.GetLeftMargin() - m.GetRightMargin()
}

// GetRenderHeight returns the height to use for rendering.
// In multi-client mode, this is the minimum of the terminal height and
// the effective session height (min of all connected clients).
func (m *OS) GetRenderHeight() int {
	// If terminal size not yet known, use effective size if available
	if m.Height == 0 {
		if m.EffectiveHeight > 0 {
			return m.EffectiveHeight
		}
		return 0
	}
	// Use minimum of terminal and effective size
	if m.EffectiveHeight > 0 && m.EffectiveHeight < m.Height {
		return m.EffectiveHeight
	}
	return m.Height
}
