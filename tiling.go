package main

import "time"

// TileAllWindows arranges all visible windows in a tiling layout
func (m *OS) TileAllWindows() {
	// Get list of visible windows in current workspace (not minimized)
	var visibleWindows []*Window
	var visibleIndices []int
	for i, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.Minimizing {
			visibleWindows = append(visibleWindows, w)
			visibleIndices = append(visibleIndices, i)
		}
	}

	if len(visibleWindows) == 0 {
		return
	}

	// Calculate tiling layout based on number of windows
	layouts := m.calculateTilingLayout(len(visibleWindows))

	// Apply layout with animations
	for i, idx := range visibleIndices {
		if i >= len(layouts) {
			break
		}

		layout := layouts[i]

		// Create animation for smooth transition
		anim := &Animation{
			WindowIndex: idx,
			Type:        AnimationSnap,
			StartTime:   time.Now(),
			Duration:    time.Duration(DefaultAnimationDuration) * time.Millisecond,
			StartX:      m.Windows[idx].X,
			StartY:      m.Windows[idx].Y,
			StartWidth:  m.Windows[idx].Width,
			StartHeight: m.Windows[idx].Height,
			EndX:        layout.x,
			EndY:        layout.y,
			EndWidth:    layout.width,
			EndHeight:   layout.height,
			Progress:    0,
			Complete:    false,
		}

		m.Animations = append(m.Animations, anim)
	}
}

type tileLayout struct {
	x, y, width, height int
}

// calculateTilingLayout returns optimal positions for n windows
func (m *OS) calculateTilingLayout(n int) []tileLayout {
	if n == 0 {
		return nil
	}

	usableHeight := m.GetUsableHeight()
	layouts := make([]tileLayout, 0, n)

	switch n {
	case 1:
		// Single window - full screen
		layouts = append(layouts, tileLayout{
			x:      0,
			y:      0,
			width:  m.Width,
			height: usableHeight,
		})

	case 2:
		// Two windows - side by side
		halfWidth := m.Width / 2
		layouts = append(layouts,
			tileLayout{
				x:      0,
				y:      0,
				width:  halfWidth,
				height: usableHeight,
			},
			tileLayout{
				x:      halfWidth,
				y:      0,
				width:  m.Width - halfWidth,
				height: usableHeight,
			},
		)

	case 3:
		// Three windows - one left, two right stacked
		halfWidth := m.Width / 2
		halfHeight := usableHeight / 2
		layouts = append(layouts,
			tileLayout{
				x:      0,
				y:      0,
				width:  halfWidth,
				height: usableHeight,
			},
			tileLayout{
				x:      halfWidth,
				y:      0,
				width:  m.Width - halfWidth,
				height: halfHeight,
			},
			tileLayout{
				x:      halfWidth,
				y:      halfHeight,
				width:  m.Width - halfWidth,
				height: usableHeight - halfHeight,
			},
		)

	case 4:
		// Four windows - 2x2 grid
		halfWidth := m.Width / 2
		halfHeight := usableHeight / 2
		layouts = append(layouts,
			tileLayout{
				x:      0,
				y:      0,
				width:  halfWidth,
				height: halfHeight,
			},
			tileLayout{
				x:      halfWidth,
				y:      0,
				width:  m.Width - halfWidth,
				height: halfHeight,
			},
			tileLayout{
				x:      0,
				y:      halfHeight,
				width:  halfWidth,
				height: usableHeight - halfHeight,
			},
			tileLayout{
				x:      halfWidth,
				y:      halfHeight,
				width:  m.Width - halfWidth,
				height: usableHeight - halfHeight,
			},
		)

	default:
		// More than 4 windows - create a grid
		// Calculate optimal grid dimensions
		cols := 3
		if n <= 6 {
			cols = 2
		}
		rows := (n + cols - 1) / cols // Ceiling division

		cellWidth := m.Width / cols
		cellHeight := usableHeight / rows

		for i := range n {
			row := i / cols
			col := i % cols

			// Last row might have fewer windows, so expand them
			actualCols := cols
			if row == rows-1 {
				remainingWindows := n - row*cols
				if remainingWindows < cols {
					actualCols = remainingWindows
					cellWidth = m.Width / actualCols
				}
			}

			layout := tileLayout{
				x:      col * cellWidth,
				y:      row * cellHeight,
				width:  cellWidth,
				height: cellHeight,
			}

			// Adjust last column width to fill screen
			if col == actualCols-1 {
				layout.width = m.Width - layout.x
			}
			// Adjust last row height to fill screen
			if row == rows-1 {
				layout.height = usableHeight - layout.y
			}

			layouts = append(layouts, layout)
		}
	}

	// Ensure minimum window size
	for i := range layouts {
		if layouts[i].width < DefaultWindowWidth {
			layouts[i].width = DefaultWindowWidth
		}
		if layouts[i].height < DefaultWindowHeight {
			layouts[i].height = DefaultWindowHeight
		}
	}

	return layouts
}

// ToggleAutoTiling toggles automatic tiling mode
func (m *OS) ToggleAutoTiling() {
	m.AutoTiling = !m.AutoTiling

	if m.AutoTiling {
		// When enabling, tile all existing windows
		m.TileAllWindows()
	}
}

// TileNewWindow arranges the new window in the tiling layout
func (m *OS) TileNewWindow() {
	if !m.AutoTiling {
		return
	}

	// Retile all windows including the new one
	m.TileAllWindows()
}

// RetileAfterClose handles window close in tiling mode
func (m *OS) RetileAfterClose() {
	if !m.AutoTiling {
		return
	}

	// Retile remaining windows
	m.TileAllWindows()
}

// SwapWindows swaps the positions of two windows with animation
func (m *OS) SwapWindows(index1, index2 int) {
	if index1 < 0 || index1 >= len(m.Windows) || index2 < 0 || index2 >= len(m.Windows) {
		return
	}

	window1 := m.Windows[index1]
	window2 := m.Windows[index2]

	// Store the positions for swapping
	x1, y1, w1, h1 := window1.X, window1.Y, window1.Width, window1.Height
	x2, y2, w2, h2 := window2.X, window2.Y, window2.Width, window2.Height

	// Create animations for both windows to swap positions
	anim1 := &Animation{
		WindowIndex: index1,
		Type:        AnimationSnap,
		StartTime:   time.Now(),
		Duration:    time.Duration(FastAnimationDuration) * time.Millisecond,
		StartX:      x1,
		StartY:      y1,
		StartWidth:  w1,
		StartHeight: h1,
		EndX:        x2,
		EndY:        y2,
		EndWidth:    w2,
		EndHeight:   h2,
		Progress:    0,
		Complete:    false,
	}

	anim2 := &Animation{
		WindowIndex: index2,
		Type:        AnimationSnap,
		StartTime:   time.Now(),
		Duration:    time.Duration(FastAnimationDuration) * time.Millisecond,
		StartX:      x2,
		StartY:      y2,
		StartWidth:  w2,
		StartHeight: h2,
		EndX:        x1,
		EndY:        y1,
		EndWidth:    w1,
		EndHeight:   h1,
		Progress:    0,
		Complete:    false,
	}

	m.Animations = append(m.Animations, anim1, anim2)
}

// SwapWindowsInstant swaps the positions of two windows instantly without animation
func (m *OS) SwapWindowsInstant(index1, index2 int) {
	if index1 < 0 || index1 >= len(m.Windows) || index2 < 0 || index2 >= len(m.Windows) {
		return
	}

	window1 := m.Windows[index1]
	window2 := m.Windows[index2]

	// Store the positions for swapping
	x1, y1, w1, h1 := window1.X, window1.Y, window1.Width, window1.Height
	x2, y2, w2, h2 := window2.X, window2.Y, window2.Width, window2.Height

	// Swap positions instantly
	window1.X = x2
	window1.Y = y2
	window1.Width = w2
	window1.Height = h2
	window1.Resize(w2, h2)
	window1.MarkPositionDirty()
	window1.InvalidateCache()

	window2.X = x1
	window2.Y = y1
	window2.Width = w1
	window2.Height = h1
	window2.Resize(w1, h1)
	window2.MarkPositionDirty()
	window2.InvalidateCache()
}

// SwapWindowsWithOriginal swaps windows where the dragged window's original position is provided
func (m *OS) SwapWindowsWithOriginal(draggedIndex, targetIndex int, origX, origY, origWidth, origHeight int) {
	if draggedIndex < 0 || draggedIndex >= len(m.Windows) || targetIndex < 0 || targetIndex >= len(m.Windows) {
		return
	}

	draggedWindow := m.Windows[draggedIndex]
	targetWindow := m.Windows[targetIndex]

	// Dragged window goes to target's position
	anim1 := &Animation{
		WindowIndex: draggedIndex,
		Type:        AnimationSnap,
		StartTime:   time.Now(),
		Duration:    time.Duration(FastAnimationDuration) * time.Millisecond,
		StartX:      draggedWindow.X, // Current dragged position
		StartY:      draggedWindow.Y,
		StartWidth:  draggedWindow.Width,
		StartHeight: draggedWindow.Height,
		EndX:        targetWindow.X, // Target's position
		EndY:        targetWindow.Y,
		EndWidth:    targetWindow.Width,
		EndHeight:   targetWindow.Height,
		Progress:    0,
		Complete:    false,
	}

	// Target window goes to dragged window's ORIGINAL position
	anim2 := &Animation{
		WindowIndex: targetIndex,
		Type:        AnimationSnap,
		StartTime:   time.Now(),
		Duration:    time.Duration(FastAnimationDuration) * time.Millisecond,
		StartX:      targetWindow.X,
		StartY:      targetWindow.Y,
		StartWidth:  targetWindow.Width,
		StartHeight: targetWindow.Height,
		EndX:        origX, // Original position of dragged window
		EndY:        origY,
		EndWidth:    origWidth,
		EndHeight:   origHeight,
		Progress:    0,
		Complete:    false,
	}

	m.Animations = append(m.Animations, anim1, anim2)
}

// SwapWithLeft swaps the focused window with the window to its left
func (m *OS) SwapWithLeft() {
	if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return
	}

	// Don't swap if animations are in progress
	if m.HasActiveAnimations() {
		return
	}

	focusedWindow := m.Windows[m.FocusedWindow]

	// Find the window to the left
	targetIndex := -1
	minDistance := m.Width

	for i, window := range m.Windows {
		if i == m.FocusedWindow || window.Minimized || window.Minimizing {
			continue
		}

		// Check if window is to the left
		if window.X+window.Width <= focusedWindow.X+5 {
			// Check if it overlaps vertically
			if window.Y < focusedWindow.Y+focusedWindow.Height &&
				window.Y+window.Height > focusedWindow.Y {
				// Find the closest one
				distance := focusedWindow.X - (window.X + window.Width)
				if distance < minDistance {
					minDistance = distance
					targetIndex = i
				}
			}
		}
	}

	if targetIndex >= 0 {
		// Swap instantly without animation for keyboard shortcuts
		m.SwapWindowsInstant(m.FocusedWindow, targetIndex)
	}
}

// SwapWithRight swaps the focused window with the window to its right
func (m *OS) SwapWithRight() {
	if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return
	}

	// Don't swap if animations are in progress
	if m.HasActiveAnimations() {
		return
	}

	focusedWindow := m.Windows[m.FocusedWindow]

	// Find the window to the right
	targetIndex := -1
	minDistance := m.Width

	for i, window := range m.Windows {
		if i == m.FocusedWindow || window.Minimized || window.Minimizing {
			continue
		}

		// Check if window is to the right
		if window.X >= focusedWindow.X+focusedWindow.Width-5 {
			// Check if it overlaps vertically
			if window.Y < focusedWindow.Y+focusedWindow.Height &&
				window.Y+window.Height > focusedWindow.Y {
				// Find the closest one
				distance := window.X - (focusedWindow.X + focusedWindow.Width)
				if distance < minDistance {
					minDistance = distance
					targetIndex = i
				}
			}
		}
	}

	if targetIndex >= 0 {
		// Swap instantly without animation for keyboard shortcuts
		m.SwapWindowsInstant(m.FocusedWindow, targetIndex)
	}
}

// SwapWithUp swaps the focused window with the window above it
func (m *OS) SwapWithUp() {
	if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return
	}

	// Don't swap if animations are in progress
	if m.HasActiveAnimations() {
		return
	}

	focusedWindow := m.Windows[m.FocusedWindow]

	// Find the window above
	targetIndex := -1
	minDistance := m.Height

	for i, window := range m.Windows {
		if i == m.FocusedWindow || window.Minimized || window.Minimizing {
			continue
		}

		// Check if window is above
		if window.Y+window.Height <= focusedWindow.Y+5 {
			// Check if it overlaps horizontally
			if window.X < focusedWindow.X+focusedWindow.Width &&
				window.X+window.Width > focusedWindow.X {
				// Find the closest one
				distance := focusedWindow.Y - (window.Y + window.Height)
				if distance < minDistance {
					minDistance = distance
					targetIndex = i
				}
			}
		}
	}

	if targetIndex >= 0 {
		// Swap instantly without animation for keyboard shortcuts
		m.SwapWindowsInstant(m.FocusedWindow, targetIndex)
	}
}

// SwapWithDown swaps the focused window with the window below it
func (m *OS) SwapWithDown() {
	if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return
	}

	// Don't swap if animations are in progress
	if m.HasActiveAnimations() {
		return
	}

	focusedWindow := m.Windows[m.FocusedWindow]

	// Find the window below
	targetIndex := -1
	minDistance := m.Height

	for i, window := range m.Windows {
		if i == m.FocusedWindow || window.Minimized || window.Minimizing {
			continue
		}

		// Check if window is below
		if window.Y >= focusedWindow.Y+focusedWindow.Height-5 {
			// Check if it overlaps horizontally
			if window.X < focusedWindow.X+focusedWindow.Width &&
				window.X+window.Width > focusedWindow.X {
				// Find the closest one
				distance := window.Y - (focusedWindow.Y + focusedWindow.Height)
				if distance < minDistance {
					minDistance = distance
					targetIndex = i
				}
			}
		}
	}

	if targetIndex >= 0 {
		// Swap instantly without animation for keyboard shortcuts
		m.SwapWindowsInstant(m.FocusedWindow, targetIndex)
	}
}

// TileRemainingWindows tiles all windows except the one being minimized
func (m *OS) TileRemainingWindows(excludeIndex int) {
	// Get list of visible windows (not minimized and not the one being minimized)
	var visibleWindows []*Window
	var visibleIndices []int
	for i, w := range m.Windows {
		if i != excludeIndex && !w.Minimized && !w.Minimizing {
			visibleWindows = append(visibleWindows, w)
			visibleIndices = append(visibleIndices, i)
		}
	}

	if len(visibleWindows) == 0 {
		return
	}

	// Calculate tiling layout based on number of remaining windows
	layouts := m.calculateTilingLayout(len(visibleWindows))

	// Apply layout with animations
	for i, idx := range visibleIndices {
		if i >= len(layouts) {
			break
		}

		layout := layouts[i]

		// Create animation for smooth transition
		anim := &Animation{
			WindowIndex: idx,
			Type:        AnimationSnap,
			StartTime:   time.Now(),
			Duration:    time.Duration(DefaultAnimationDuration) * time.Millisecond,
			StartX:      m.Windows[idx].X,
			StartY:      m.Windows[idx].Y,
			StartWidth:  m.Windows[idx].Width,
			StartHeight: m.Windows[idx].Height,
			EndX:        layout.x,
			EndY:        layout.y,
			EndWidth:    layout.width,
			EndHeight:   layout.height,
			Progress:    0,
			Complete:    false,
		}

		m.Animations = append(m.Animations, anim)
	}
}
