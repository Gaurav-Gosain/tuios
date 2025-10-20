package app

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	tea "github.com/charmbracelet/bubbletea/v2"
)

// TickerMsg represents a periodic tick event for updating the UI.
// This is exported so it can be used by the input package.
type TickerMsg time.Time

// WindowExitMsg signals that a terminal window process has exited.
// This is exported so it can be used by the input package.
type WindowExitMsg struct {
	WindowID string
}

// InputHandler is a function type that handles input messages.
// This allows the Update method to delegate to the input package without creating a circular dependency.
type InputHandler func(msg tea.Msg, o *OS) (tea.Model, tea.Cmd)

// inputHandler is the registered input handler function.
// This will be set by the main package to break the circular dependency.
var inputHandler InputHandler

// SetInputHandler registers the input handler function.
// This must be called during initialization before the Update loop runs.
func SetInputHandler(handler InputHandler) {
	inputHandler = handler
}

// Init initializes the TUIOS application and returns initial commands to run.
// It enables mouse tracking, starts the tick timer, and listens for window exits.
func (m *OS) Init() tea.Cmd {
	return tea.Batch(
		tea.EnableMouseAllMotion,
		tea.EnableBracketedPaste,     // Better paste handling for the main app
		tea.EnableGraphemeClustering, // Better Unicode support
		tea.EnableReportFocus,        // Track when terminal gains/loses focus
		TickCmd(),
		ListenForWindowExits(m.WindowExitChan),
	)
}

// ListenForWindowExits creates a command that listens for window process exits.
// It safely reads from the exit channel and converts exit signals to messages.
func ListenForWindowExits(exitChan chan string) tea.Cmd {
	return func() tea.Msg {
		// Safe channel read with protection against closed channel
		windowID, ok := <-exitChan
		if !ok {
			// Channel closed, return nil to stop listening
			return nil
		}
		return WindowExitMsg{WindowID: windowID}
	}
}

// TickCmd creates a command that generates tick messages at 60 FPS.
// This drives the main update loop for animations and terminal content updates.
func TickCmd() tea.Cmd {
	return tea.Tick(time.Second/config.NormalFPS, func(t time.Time) tea.Msg {
		return TickerMsg(t)
	})
}

// SlowTickCmd creates a command that generates tick messages at 30 FPS.
// Used during user interactions to improve responsiveness.
func SlowTickCmd() tea.Cmd {
	return tea.Tick(time.Second/config.InteractionFPS, func(t time.Time) tea.Msg {
		return TickerMsg(t)
	})
}

// Update handles all incoming messages and updates the application state.
// It processes keyboard, mouse, and timer events, managing windows and UI updates.
func (m *OS) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case TickerMsg:
		// Update animations
		m.UpdateAnimations()

		// Update system info
		m.UpdateCPUHistory()

		// Adaptive polling - slower during interactions for better mouse responsiveness
		hasChanges := m.MarkTerminalsWithNewContent()

		// Check if we have active animations
		hasAnimations := m.HasActiveAnimations()

		// Determine tick rate based on interaction mode
		nextTick := TickCmd()
		if m.InteractionMode {
			nextTick = SlowTickCmd() // 30 FPS during interactions
		}

		// Skip rendering if no changes, no animations, and not in interaction mode (frame skipping)
		if !hasChanges && !hasAnimations && !m.InteractionMode && len(m.Windows) > 0 {
			// Continue ticking but don't trigger render
			return m, nextTick
		}

		return m, nextTick

	case WindowExitMsg:
		// Handle window exit - find and close the window
		for i := range m.Windows {
			if m.Windows[i].ID == msg.WindowID {
				m.DeleteWindow(i)
				break
			}
		}
		// Ensure we're in window management mode if no windows remain
		if len(m.Windows) == 0 {
			m.Mode = WindowManagementMode
		}
		return m, ListenForWindowExits(m.WindowExitChan)

	case tea.KeyPressMsg, tea.MouseClickMsg, tea.MouseMotionMsg,
		tea.MouseReleaseMsg, tea.MouseWheelMsg, tea.ClipboardMsg:
		// Delegate to the registered input handler
		if inputHandler != nil {
			return inputHandler(msg, m)
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height
		m.MarkAllDirty()

		// Retile windows if in tiling mode
		if m.AutoTiling {
			m.TileAllWindows()
		}

		return m, nil

	case tea.MouseMsg:
		// Catch-all for any other mouse events to prevent them from leaking
		return m, nil

	case tea.FocusMsg:
		// Terminal gained focus
		// Could be used to refresh or resume operations
		return m, nil

	case tea.BlurMsg:
		// Terminal lost focus
		// Could be used to pause expensive operations
		return m, nil
	}

	return m, nil
}
