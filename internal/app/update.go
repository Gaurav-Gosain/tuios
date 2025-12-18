package app

import (
	"fmt"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
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

// ScriptCommandMsg represents a command from a tape script to be executed.
// This allows tape commands to be processed through the normal message handling flow.
type ScriptCommandMsg struct {
	Command *tape.Command
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
// It starts the tick timer and listens for window exits.
// Note: Mouse tracking, bracketed paste, and focus reporting are now configured
// in the View() method as per bubbletea v2.0.0-beta.5 API changes.
func (m *OS) Init() tea.Cmd {
	cmds := []tea.Cmd{
		TickCmd(),
		ListenForWindowExits(m.WindowExitChan),
	}

	// If this is a restored daemon session, enable callbacks after a delay
	// This allows buffered PTY output to settle before callbacks start tracking changes
	if m.IsDaemonSession && m.RestoredFromState {
		cmds = append(cmds, EnableCallbacksAfterDelay())
		// Trigger alt screen redraws immediately to force apps like btop to redraw
		cmds = append(cmds, TriggerAltScreenRedrawCmd())
	}

	return tea.Batch(cmds...)
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

// EnableCallbacksMsg is sent after a delay to re-enable VT emulator callbacks
// after restoring a daemon session.
type EnableCallbacksMsg struct{}

// EnableCallbacksAfterDelay returns a command that waits briefly then sends
// a message to re-enable callbacks after buffered output has settled.
func EnableCallbacksAfterDelay() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return EnableCallbacksMsg{}
	})
}

// TriggerAltScreenRedrawMsg triggers alt screen apps to redraw.
type TriggerAltScreenRedrawMsg struct{}

// TriggerAltScreenRedrawCmd returns a command that immediately triggers
// alt screen apps (vim, htop, btop) to redraw via SIGWINCH.
func TriggerAltScreenRedrawCmd() tea.Cmd {
	return func() tea.Msg {
		return TriggerAltScreenRedrawMsg{}
	}
}

// Update handles all incoming messages and updates the application state.
// It processes keyboard, mouse, and timer events, managing windows and UI updates.
func (m *OS) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case TickerMsg:
		// Proactively check for exited processes and clean them up
		// This ensures windows close even if the exit channel message was missed
		for i := len(m.Windows) - 1; i >= 0; i-- {
			if m.Windows[i].ProcessExited {
				m.DeleteWindow(i)
			}
		}

		// Update animations
		m.UpdateAnimations()

		// Update system info
		m.UpdateCPUHistory()
		m.UpdateRAMUsage()

		// Handle script playback if in script mode
		cmds := []tea.Cmd{TickCmd()}
		if m.ScriptMode && !m.ScriptPaused && m.ScriptPlayer != nil {
			player, ok := m.ScriptPlayer.(*tape.Player)
			if ok && !player.IsFinished() {
				// Wait for animations to complete before executing next command
				// This ensures visual consistency during script playback
				if m.HasActiveAnimations() {
					return m, TickCmd()
				}

				// Check if we're waiting for a sleep to finish
				if !m.ScriptSleepUntil.IsZero() && time.Now().Before(m.ScriptSleepUntil) {
					// Still waiting, don't advance yet
					return m, TickCmd()
				}
				// Sleep finished or wasn't waiting, clear the sleep time
				m.ScriptSleepUntil = time.Time{}

				nextCmd := player.NextCommand()
				if nextCmd != nil {
					// Handle Sleep commands specially
					if nextCmd.Type == tape.CommandTypeSleep && nextCmd.Delay > 0 {
						// Set the sleep deadline
						m.ScriptSleepUntil = time.Now().Add(nextCmd.Delay)
						// Advance to next command but don't execute anything yet
						player.Advance()
					} else {
						// Queue the command as a message instead of executing directly
						cmds = append(cmds, func() tea.Msg {
							return ScriptCommandMsg{Command: nextCmd}
						})
						// Advance to next command
						player.Advance()
					}
				}
			} else if ok && player.IsFinished() {
				// Script just finished - record the time if not already set
				if m.ScriptFinishedTime.IsZero() {
					m.ScriptFinishedTime = time.Now()
				}
			}
		}

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
			if len(cmds) > 1 {
				return m, tea.Sequence(cmds...)
			}
			return m, nextTick
		}

		if len(cmds) > 1 {
			return m, tea.Sequence(cmds...)
		}
		return m, nextTick

	case WindowExitMsg:
		windowID := msg.WindowID
		for i, w := range m.Windows {
			if w.ID == windowID {
				m.DeleteWindow(i)
				break
			}
		}
		// Ensure we're in window management mode if no windows remain
		if len(m.Windows) == 0 {
			m.Mode = WindowManagementMode
		}
		return m, ListenForWindowExits(m.WindowExitChan)

	case EnableCallbacksMsg:
		// Re-enable VT emulator callbacks after buffered output has settled
		// This prevents the race condition where buffered PTY output overwrites
		// the restored IsAltScreen state
		m.LogInfo("[CALLBACKS] Re-enabling callbacks for all windows")
		for _, w := range m.Windows {
			if w.DaemonMode {
				w.EnableCallbacks()
				m.LogInfo("[CALLBACKS] Enabled for window %s (IsAltScreen=%v)", w.ID[:8], w.IsAltScreen)
			}
		}
		return m, nil

	case TriggerAltScreenRedrawMsg:
		// Force alt screen apps to redraw by sending resize (fake then real)
		// This triggers SIGWINCH which makes apps like vim/htop/btop redraw
		m.LogInfo("[REDRAW] Triggering alt screen redraws")
		for _, w := range m.Windows {
			if w.DaemonMode && w.IsAltScreen && w.DaemonResizeFunc != nil {
				termWidth := max(w.Width-2, 1)
				termHeight := max(w.Height-2, 1)

				// Do a fake resize to slightly smaller, then back to real size
				// This ensures SIGWINCH is sent even if size "hasn't changed"
				fakeWidth := max(termWidth-1, 1)
				fakeHeight := max(termHeight-1, 1)

				_ = w.DaemonResizeFunc(fakeWidth, fakeHeight)
				_ = w.DaemonResizeFunc(termWidth, termHeight)

				w.InvalidateCache()
				w.MarkContentDirty()
				m.LogInfo("[REDRAW] Sent resize to window %s (%dx%d)", w.ID[:8], termWidth, termHeight)
			}
		}
		m.MarkAllDirty()
		return m, nil

	case tea.KeyPressMsg, tea.MouseClickMsg, tea.MouseMotionMsg,
		tea.MouseReleaseMsg, tea.MouseWheelMsg, tea.ClipboardMsg,
		tea.PasteMsg, tea.PasteStartMsg, tea.PasteEndMsg:
		// Delegate to the registered input handler
		if inputHandler != nil {
			return inputHandler(msg, m)
		}
		return m, nil

	case tea.WindowSizeMsg:
		oldWidth, oldHeight := m.Width, m.Height
		m.Width = msg.Width
		m.Height = msg.Height
		m.MarkAllDirty()

		// When restored from state, we need to retile if tiling is enabled
		// to properly fit windows to the new terminal size.
		// The BSP tree structure is preserved, only positions/sizes are recalculated.
		if m.RestoredFromState {
			m.RestoredFromState = false
			if m.AutoTiling {
				m.LogInfo("[RESIZE] Retiling restored session to fit new terminal size (%dx%d -> %dx%d)",
					oldWidth, oldHeight, msg.Width, msg.Height)
				m.TileAllWindows()
			}
			return m, nil
		}

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

	case tea.KeyboardEnhancementsMsg:
		// Keyboard enhancements enabled - terminal supports Kitty protocol
		// This enables better key disambiguation and international keyboard support
		m.KeyboardEnhancementsEnabled = msg.SupportsKeyDisambiguation()
		if m.KeyboardEnhancementsEnabled {
			m.ShowNotification("Keyboard enhancements enabled", "info", config.NotificationDuration)
		}
		return m, nil

	case ScriptCommandMsg:
		// Execute tape command through the executor
		if executor, ok := m.ScriptExecutor.(*tape.CommandExecutor); ok {
			if err := executor.Execute(msg.Command); err != nil {
				// Log error but continue playback
				m.ShowNotification(fmt.Sprintf("Script error: %v", err), "error", config.NotificationDuration)
			}
		}
		return m, nil

	}

	return m, nil
}
