package app

import (
	"fmt"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
)

// The following methods implement the tape.Executor interface for
// scripted automation and tape playback functionality.

// ExecuteCommand executes a tape command.
func (m *OS) ExecuteCommand(_ *tape.Command) error {
	return nil
}

// GetFocusedWindowID returns the ID of the focused window.
func (m *OS) GetFocusedWindowID() string {
	if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
		return m.Windows[m.FocusedWindow].ID
	}
	return ""
}

// SendToWindow sends bytes to a window's PTY.
func (m *OS) SendToWindow(windowID string, data []byte) error {
	for _, w := range m.Windows {
		if w.ID == windowID {
			_, err := w.Pty.Write(data)
			return err
		}
	}
	return nil
}

// CreateNewWindow creates a new window.
func (m *OS) CreateNewWindow() error {
	m.AddWindow("Window")
	m.MarkAllDirty()
	return nil
}

// CloseWindow closes a window.
func (m *OS) CloseWindow(windowID string) error {
	for i, w := range m.Windows {
		if w.ID == windowID {
			m.DeleteWindow(i)
			m.MarkAllDirty()
			return nil
		}
	}
	return nil
}

// SwitchWorkspace switches to a workspace.
func (m *OS) SwitchWorkspace(workspace int) error {
	if workspace >= 1 && workspace <= m.NumWorkspaces {
		recorder := m.TapeRecorder
		m.TapeRecorder = nil
		m.SwitchToWorkspace(workspace)
		m.TapeRecorder = recorder
		m.MarkAllDirty()
	}
	return nil
}

// ToggleTiling toggles tiling mode.
func (m *OS) ToggleTiling() error {
	m.AutoTiling = !m.AutoTiling
	if m.AutoTiling {
		m.TileAllWindows()
	}
	m.MarkAllDirty()
	return nil
}

// SetMode sets the interaction mode.
func (m *OS) SetMode(mode string) error {
	switch mode {
	case "terminal", "Terminal", "TerminalMode":
		m.Mode = TerminalMode
		if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
			for i, w := range m.Windows {
				if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.Minimizing {
					m.FocusWindow(i)
					break
				}
			}
		}
	case "window", "Window", "WindowManagementMode":
		m.Mode = WindowManagementMode
	}
	return nil
}

// NextWindow focuses the next window.
func (m *OS) NextWindow() error {
	if len(m.Windows) == 0 {
		return nil
	}
	m.CycleToNextVisibleWindow()
	m.MarkAllDirty()
	return nil
}

// PrevWindow focuses the previous window.
func (m *OS) PrevWindow() error {
	if len(m.Windows) == 0 {
		return nil
	}
	m.CycleToPreviousVisibleWindow()
	m.MarkAllDirty()
	return nil
}

// FocusWindowByID focuses a specific window by ID.
func (m *OS) FocusWindowByID(windowID string) error {
	for i, w := range m.Windows {
		if w.ID == windowID {
			m.FocusWindow(i)
			m.MarkAllDirty()
			return nil
		}
	}
	return nil
}

// RenameWindowByID renames a window by its ID.
func (m *OS) RenameWindowByID(windowID, name string) error {
	for _, w := range m.Windows {
		if w.ID == windowID {
			w.Title = name
			m.MarkAllDirty()
			return nil
		}
	}
	return nil
}

// MinimizeWindowByID minimizes a window.
func (m *OS) MinimizeWindowByID(windowID string) error {
	for i, w := range m.Windows {
		if w.ID == windowID {
			m.MinimizeWindow(i)
			m.MarkAllDirty()
			return nil
		}
	}
	return nil
}

// RestoreWindowByID restores a minimized window.
func (m *OS) RestoreWindowByID(windowID string) error {
	for i, w := range m.Windows {
		if w.ID == windowID {
			m.RestoreWindow(i)
			m.MarkAllDirty()
			return nil
		}
	}
	return nil
}

// EnableTiling enables tiling mode.
func (m *OS) EnableTiling() error {
	if !m.AutoTiling {
		m.AutoTiling = true
		m.TileAllWindows()
		m.MarkAllDirty()
	}
	return nil
}

// DisableTiling disables tiling mode.
func (m *OS) DisableTiling() error {
	m.AutoTiling = false
	m.MarkAllDirty()
	return nil
}

// SnapByDirection snaps a window to a direction.
func (m *OS) SnapByDirection(direction string) error {
	if m.AutoTiling {
		return fmt.Errorf("cannot snap windows while tiling mode is enabled")
	}

	if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return nil
	}

	quarter := SnapTopLeft
	switch direction {
	case "left":
		quarter = SnapLeft
	case "right":
		quarter = SnapRight
	case "fullscreen":
		m.Snap(m.FocusedWindow, SnapTopLeft)
		m.MarkAllDirty()
		return nil
	}

	m.Snap(m.FocusedWindow, quarter)
	m.MarkAllDirty()
	return nil
}

// MoveWindowToWorkspaceByID moves a window to a workspace.
func (m *OS) MoveWindowToWorkspaceByID(windowID string, workspace int) error {
	if workspace < 1 || workspace > m.NumWorkspaces {
		return nil
	}

	for i, w := range m.Windows {
		if w.ID == windowID {
			w.Workspace = workspace
			if m.FocusedWindow == i {
				m.FocusedWindow = -1
			}
			if m.AutoTiling {
				m.TileAllWindows()
			}
			m.MarkAllDirty()
			return nil
		}
	}

	return nil
}

// MoveAndFollowWorkspaceByID moves a window to a workspace and switches to it.
func (m *OS) MoveAndFollowWorkspaceByID(windowID string, workspace int) error {
	if workspace < 1 || workspace > m.NumWorkspaces {
		return nil
	}

	for _, w := range m.Windows {
		if w.ID == windowID {
			w.Workspace = workspace
			m.CurrentWorkspace = workspace
			m.FocusedWindow = -1
			if m.AutoTiling {
				m.TileAllWindows()
			}
			m.MarkAllDirty()
			return nil
		}
	}

	return nil
}

// SplitHorizontal splits the focused window horizontally.
func (m *OS) SplitHorizontal() error {
	if !m.AutoTiling {
		return nil
	}
	m.SplitFocusedHorizontal()
	m.MarkAllDirty()
	return nil
}

// SplitVertical splits the focused window vertically.
func (m *OS) SplitVertical() error {
	if !m.AutoTiling {
		return nil
	}
	m.SplitFocusedVertical()
	m.MarkAllDirty()
	return nil
}

// RotateSplit rotates the split direction at the focused window.
func (m *OS) RotateSplit() error {
	if !m.AutoTiling {
		return nil
	}
	m.RotateFocusedSplit()
	m.MarkAllDirty()
	return nil
}

// EqualizeSplitsExec equalizes all split ratios.
func (m *OS) EqualizeSplitsExec() error {
	if !m.AutoTiling {
		return nil
	}
	m.EqualizeSplits()
	m.MarkAllDirty()
	return nil
}

// Preselect sets the preselection direction for the next window.
func (m *OS) Preselect(direction string) error {
	if !m.AutoTiling {
		return nil
	}
	switch direction {
	case "left":
		m.SetPreselection(layout.PreselectionLeft)
	case "right":
		m.SetPreselection(layout.PreselectionRight)
	case "up":
		m.SetPreselection(layout.PreselectionUp)
	case "down":
		m.SetPreselection(layout.PreselectionDown)
	default:
		m.ClearPreselection()
	}
	return nil
}

// EnableAnimations enables UI animations.
func (m *OS) EnableAnimations() error {
	config.AnimationsEnabled = true
	m.ShowNotification("Animations: ON", "info", config.NotificationDuration)
	return nil
}

// DisableAnimations disables UI animations.
func (m *OS) DisableAnimations() error {
	config.AnimationsEnabled = false
	m.ShowNotification("Animations: OFF", "info", config.NotificationDuration)
	return nil
}

// ToggleAnimations toggles UI animations.
func (m *OS) ToggleAnimations() error {
	config.AnimationsEnabled = !config.AnimationsEnabled
	if config.AnimationsEnabled {
		m.ShowNotification("Animations: ON", "info", config.NotificationDuration)
	} else {
		m.ShowNotification("Animations: OFF", "info", config.NotificationDuration)
	}
	return nil
}
