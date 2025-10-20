package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
)

// CreateMinimizeAnimation creates a minimize animation for the window at index i
func (m *OS) CreateMinimizeAnimation(i int) *ui.Animation {
	if i < 0 || i >= len(m.Windows) {
		return nil
	}

	window := m.Windows[i]

	// Calculate dock position for this window
	dockX, dockY := m.calculateDockPosition(i)

	return ui.NewMinimizeAnimation(window, dockX, dockY, config.DefaultAnimationDuration)
}

// CreateRestoreAnimation creates a restore animation for the window at index i
func (m *OS) CreateRestoreAnimation(i int) *ui.Animation {
	if i < 0 || i >= len(m.Windows) {
		return nil
	}

	window := m.Windows[i]

	// Calculate dock position for this window
	dockX, dockY := m.calculateDockPosition(i)

	return ui.NewRestoreAnimation(window, dockX, dockY, config.DefaultAnimationDuration)
}

// CreateSnapAnimation creates a snap animation for the window at index i
func (m *OS) CreateSnapAnimation(i int, quarter SnapQuarter) *ui.Animation {
	if i < 0 || i >= len(m.Windows) {
		return nil
	}

	window := m.Windows[i]

	// Calculate target bounds for the snap
	targetX, targetY, targetWidth, targetHeight := m.calculateSnapBounds(quarter)

	// Enforce minimum size
	targetWidth = max(targetWidth, config.DefaultWindowWidth)
	targetHeight = max(targetHeight, config.DefaultWindowHeight)

	return ui.NewSnapAnimation(window, targetX, targetY, targetWidth, targetHeight, config.DefaultAnimationDuration)
}

// HasActiveAnimations returns true if there are any active animations
func (m *OS) HasActiveAnimations() bool {
	return len(m.Animations) > 0
}

// UpdateAnimations updates all active animations and applies their effects.
func (m *OS) UpdateAnimations() {
	// Update animations in reverse order so we can safely remove completed ones
	for i := len(m.Animations) - 1; i >= 0; i-- {
		anim := m.Animations[i]

		// Update the animation and check if it's complete
		isComplete := anim.Update()

		// If animation is complete, handle post-animation logic
		if isComplete {
			// Handle minimize animation completion
			if anim.Type == ui.AnimationMinimize {
				// Find the window index for this animation
				for winIdx, win := range m.Windows {
					if win == anim.Window {
						// NOW change focus after animation completes
						if winIdx == m.FocusedWindow {
							m.FocusNextVisibleWindow()
						}
						break
					}
				}
			}

			// Remove completed animation
			m.Animations = append(m.Animations[:i], m.Animations[i+1:]...)
		}
	}
}

// calculateDockPosition calculates the position in the dock for a minimized window
func (m *OS) calculateDockPosition(windowIndex int) (int, int) {
	// Find all minimized/minimizing windows in current workspace
	dockWindows := []int{}
	targetDockIndex := -1

	for i, window := range m.Windows {
		if window.Workspace == m.CurrentWorkspace && (window.Minimized || window.Minimizing) {
			if i == windowIndex {
				targetDockIndex = len(dockWindows)
			}
			dockWindows = append(dockWindows, i)
			if len(dockWindows) >= 9 {
				break
			}
		}
	}

	// If not found in dock windows, use the next position
	if targetDockIndex == -1 {
		targetDockIndex = len(dockWindows)
	}

	// Dock is at the bottom of the screen
	dockY := m.Height - config.DockHeight + 1 // +1 for the separator line

	// Calculate dock layout (mirroring renderDock logic)
	leftWidth := 30
	rightWidth := 20

	// Estimate dock items width (each pill is approximately 6-16 chars depending on name)
	// Use conservative estimate: 8 chars per item + 1 space between
	estimatedItemWidth := 8
	estimatedTotalWidth := len(dockWindows) * estimatedItemWidth
	if len(dockWindows) > 1 {
		estimatedTotalWidth += (len(dockWindows) - 1) // spaces between items
	}

	availableSpace := m.Width - leftWidth - rightWidth - estimatedTotalWidth
	if availableSpace < 0 {
		availableSpace = 0
	}
	leftSpacer := availableSpace / 2

	// Calculate X position: start of dock items + offset for this item
	dockX := leftWidth + leftSpacer + (targetDockIndex * (estimatedItemWidth + 1))

	return dockX, dockY
}
