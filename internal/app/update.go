package app

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TickerMsg represents a periodic tick event for maintenance tasks
// (animations, dock stats, script playback). NOT for PTY-driven rendering.
type TickerMsg time.Time

// PTYDataMsg signals that one or more PTY readers have new output.
// This triggers re-rendering. Sent from the PTYDataChan listener.
type PTYDataMsg struct{}

// AutoScrollTickMsg triggers continuous scrolling while dragging outside content area.
type AutoScrollTickMsg struct{}

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

// RemoteCommandMsg represents a remote command from the CLI.
// This allows remote commands to be processed through the normal message handling flow.
type RemoteCommandMsg struct {
	CommandType string   // "tape_command", "send_keys", "set_config", "tape_script"
	TapeCommand string   // For tape commands (single command)
	TapeArgs    []string // Arguments for tape command
	TapeScript  string   // For tape_script (full script content)
	Keys        string   // For send_keys
	Literal     bool     // For send_keys (send to PTY)
	Raw         bool     // For send_keys (no splitting on space/comma)
	ConfigPath  string   // For set_config
	ConfigValue string   // For set_config
	RequestID   string   // For response tracking
}

// RemoteKeyMsg represents a single key to be processed from a remote send-keys command.
// Keys are sent one at a time to allow proper sequential processing.
type RemoteKeyMsg struct {
	Key           tea.KeyPressMsg   // The key to process
	RemainingKeys []tea.KeyPressMsg // Keys still to be processed
	RequestID     string            // For response tracking on last key
}

// RemoteKeysDoneMsg signals that all remote keys have been processed.
// This triggers a final cleanup/retile.
type RemoteKeysDoneMsg struct {
	RequestID string
}

// RemoteTapeCommandMsg represents a single tape command from a remote script.
// Commands are processed one at a time to allow proper sequential execution.
type RemoteTapeCommandMsg struct {
	Command           tape.Command   // The command to execute
	RemainingCommands []tape.Command // Commands still to be processed
	RequestID         string         // For response tracking on last command
	CommandIndex      int            // 0-based index of current command (for progress display)
	TotalCommands     int            // Total number of commands in script
}

// RemoteTapeScriptDoneMsg signals that all tape commands have been processed.
type RemoteTapeScriptDoneMsg struct {
	RequestID string
}

// Multi-client message types for daemon mode

// StateSyncMsg is sent when another client updates session state.
type StateSyncMsg struct {
	State       *session.SessionState
	TriggerType string
	SourceID    string
}

// ClientJoinedMsg is sent when another client joins the session.
type ClientJoinedMsg struct {
	ClientID    string
	ClientCount int
	Width       int
	Height      int
}

// ClientLeftMsg is sent when another client leaves the session.
type ClientLeftMsg struct {
	ClientID    string
	ClientCount int
}

// ClientEvent represents a client join or leave event for channel-based notification.
type ClientEvent struct {
	Type        string // "joined" or "left"
	ClientID    string
	ClientCount int
	Width       int // only for "joined"
	Height      int // only for "joined"
}

// SessionResizeMsg is sent when the effective session size changes (min of all clients).
type SessionResizeMsg struct {
	Width       int
	Height      int
	ClientCount int
}

// ForceRefreshMsg is sent to force all clients to re-render.
type ForceRefreshMsg struct {
	Reason string
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
		ListenForPTYData(m.PTYDataChan),
	}

	// Listen for state sync from other clients (daemon/SSH/web mode)
	if m.StateSyncChan != nil {
		cmds = append(cmds, ListenForStateSync(m.StateSyncChan))
	}

	// Listen for client join/leave events (daemon/SSH/web mode)
	if m.ClientEventChan != nil {
		cmds = append(cmds, ListenForClientEvents(m.ClientEventChan))
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

// ListenForStateSync creates a command that listens for state sync from other clients.
// It safely reads from the sync channel and converts state to messages for the update loop.
func ListenForStateSync(syncChan chan *session.SessionState) tea.Cmd {
	if syncChan == nil {
		return nil
	}
	return func() tea.Msg {
		// Safe channel read with protection against closed channel
		state, ok := <-syncChan
		if !ok {
			// Channel closed, return nil to stop listening
			return nil
		}
		return StateSyncMsg{State: state}
	}
}

// ListenForClientEvents creates a command that listens for client join/leave events.
// It safely reads from the event channel and converts events to messages for the update loop.
func ListenForClientEvents(eventChan chan ClientEvent) tea.Cmd {
	if eventChan == nil {
		return nil
	}
	return func() tea.Msg {
		// Safe channel read with protection against closed channel
		event, ok := <-eventChan
		if !ok {
			// Channel closed, return nil to stop listening
			return nil
		}
		if event.Type == "joined" {
			return ClientJoinedMsg{
				ClientID:    event.ClientID,
				ClientCount: event.ClientCount,
				Width:       event.Width,
				Height:      event.Height,
			}
		}
		return ClientLeftMsg{
			ClientID:    event.ClientID,
			ClientCount: event.ClientCount,
		}
	}
}

// TickCmd creates a maintenance tick for animations, dock stats, and script playback.
// This runs at a low rate and does NOT drive PTY rendering.
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

// IdleTickCmd creates a command that generates tick messages at 10 FPS.
// Used when the terminal has been idle for a sustained period to reduce CPU.
func IdleTickCmd() tea.Cmd {
	return tea.Tick(time.Second/config.IdleFPS, func(t time.Time) tea.Msg {
		return TickerMsg(t)
	})
}

// ListenForPTYData returns a Cmd that blocks until a PTY reader signals
// new data, then sends a PTYDataMsg to trigger re-rendering.
func autoScrollTick() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
		return AutoScrollTickMsg{}
	})
}

func ListenForPTYData(ch <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		<-ch
		return PTYDataMsg{}
	}
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
	// Any non-tick message invalidates the render cache
	if _, isTick := msg.(TickerMsg); !isTick {
		m.renderSkipped = false
	}

	switch msg := msg.(type) {
	case PTYDataMsg:
		// PTY output arrived — mark dirty terminals and re-render immediately.
		// This is the primary render trigger, replacing tick-driven rendering.
		// Graphics refresh (kitty/sixel) happens in GetCanvas during View().
		m.MarkTerminalsWithNewContent()
		m.renderSkipped = false
		return m, ListenForPTYData(m.PTYDataChan)

	case AutoScrollTickMsg:
		if !m.AutoScrollActive || m.AutoScrollDir == 0 {
			return m, nil
		}
		// Find the target window for auto-scrolling
		var w *terminal.Window
		if m.DraggedWindowIndex >= 0 && m.DraggedWindowIndex < len(m.Windows) {
			w = m.Windows[m.DraggedWindowIndex]
		}
		if w == nil {
			w = m.GetFocusedWindow()
		}
		if w != nil && w.CopyMode != nil && w.CopyMode.Active {
			cm := w.CopyMode
			for range 2 {
				if m.AutoScrollDir < 0 {
					// Scroll up: use same logic as moveUp (keep cursor mid-screen)
					midPoint := w.Height / 2
					if cm.CursorY > midPoint {
						cm.CursorY--
					} else if w.Terminal != nil && cm.ScrollOffset < w.Terminal.ScrollbackLen() {
						cm.ScrollOffset++
						w.ScrollbackOffset = cm.ScrollOffset
					} else if cm.CursorY > 0 {
						cm.CursorY--
					}
				} else {
					// Scroll down: use same logic as moveDown
					midPoint := w.Height / 2
					if cm.CursorY < midPoint {
						cm.CursorY++
					} else if cm.ScrollOffset > 0 {
						cm.ScrollOffset--
						w.ScrollbackOffset = cm.ScrollOffset
					} else if cm.CursorY < w.Height-3 {
						cm.CursorY++
					}
				}
			}
			// Update visual selection end
			if cm.State == terminal.CopyModeVisualChar || cm.State == terminal.CopyModeVisualLine {
				scrollbackLen := 0
				if w.Terminal != nil {
					scrollbackLen = w.Terminal.ScrollbackLen()
				}
				absY := scrollbackLen - cm.ScrollOffset + cm.CursorY
				cm.VisualEnd = terminal.Position{X: cm.CursorX, Y: absY}
			}
			w.Dirty = true
			w.ContentDirty = true
			w.InvalidateCache()
		}
		m.renderSkipped = false
		return m, autoScrollTick()

	case TickerMsg:
		// Maintenance tick: animations, dock stats, script playback, process cleanup.
		// Does NOT trigger rendering unless animations/interactions are active.
		// This ensures windows close even if the exit channel message was missed
		for i := len(m.Windows) - 1; i >= 0; i-- {
			if m.Windows[i].ProcessExited {
				m.DeleteWindow(i)
			}
		}

		// Update animations
		m.UpdateAnimations()

		// Update system info (only when explicitly enabled)
		if config.ShowCPU {
			m.UpdateCPUHistory()
		}
		if config.ShowRAM {
			m.UpdateRAMUsage()
		}

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

		// Tick handles animations, interactions, whichkey, dock stats, and scripts.
		// PTY content changes are handled by PTYDataMsg (event-driven).
		hasAnimations := m.HasActiveAnimations()
		needsDockTick := config.NeedsDockTick()

		// Determine next tick rate
		var nextTick tea.Cmd
		if m.InteractionMode {
			nextTick = SlowTickCmd() // 30 FPS during drag/resize
		} else if hasAnimations || m.PrefixActive || m.ScriptMode || needsDockTick {
			nextTick = TickCmd() // Normal FPS when things need periodic updates
		} else {
			nextTick = IdleTickCmd() // Slow idle tick (process cleanup, etc.)
		}
		cmds[0] = nextTick

		// Sync background windows that have accumulated output.
		// This catches windows whose HasNewOutput flag was preserved by
		// the throttling logic, ensuring they eventually render.
		hasBackgroundChanges := m.MarkTerminalsWithNewContent()

		// Render on tick if something periodic needs visual updates OR background windows changed
		needsRender := hasAnimations || m.InteractionMode || m.PrefixActive || needsDockTick || hasBackgroundChanges
		if !needsRender {
			m.renderSkipped = true
			if len(cmds) > 1 {
				return m, tea.Batch(cmds...)
			}
			return m, nextTick
		}
		m.renderSkipped = false
		// Graphics refresh (kitty/sixel) happens in GetCanvas during View().

		if len(cmds) > 1 {
			return m, tea.Batch(cmds...)
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
				termWidth := w.ContentWidth()
				termHeight := w.ContentHeight()

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
		// Reset idle counter on any user input to restore full tick rate
		m.idleFrames = 0
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

		// Notify daemon of our terminal size for multi-client size calculation
		// This allows the daemon to compute effective size = min(all clients)
		if m.IsDaemonSession && m.DaemonClient != nil {
			_ = m.DaemonClient.NotifyTerminalSize(msg.Width, msg.Height)
		}

		// When restored from state, we need to retile if tiling is enabled
		// to properly fit windows to the new terminal size.
		// The BSP tree structure is preserved, only positions/sizes are recalculated.
		// However, if the size is the same (e.g., web reload), skip retiling to preserve layout.
		if m.RestoredFromState {
			m.RestoredFromState = false
			sizeChanged := oldWidth != msg.Width || oldHeight != msg.Height
			if sizeChanged {
				// In daemon mode, don't tile here - wait for SessionResizeMsg with the correct
				// effective size (min of all clients). Tiling now would use stale EffectiveWidth/Height
				// from the initial attach handshake (typically 80x24).
				if m.IsDaemonSession && m.AutoTiling {
					m.LogInfo("[RESIZE] Daemon mode restore: waiting for SessionResizeMsg before tiling (%dx%d -> %dx%d)",
						oldWidth, oldHeight, msg.Width, msg.Height)
					// Don't tile yet - SessionResizeMsg will trigger the retiling
				} else if m.AutoTiling {
					// Non-daemon mode: tile immediately
					m.LogInfo("[RESIZE] Retiling restored session to fit new terminal size (%dx%d -> %dx%d)",
						oldWidth, oldHeight, msg.Width, msg.Height)
					m.TileAllWindows()
				} else {
					// In floating mode, scale windows proportionally if dimensions changed
					if oldWidth > 0 && oldHeight > 0 {
						m.LogInfo("[RESIZE] Scaling restored windows from %dx%d -> %dx%d",
							oldWidth, oldHeight, msg.Width, msg.Height)
						m.ScaleWindowsToTerminal(oldWidth, oldHeight, msg.Width, msg.Height)
					} else {
						// No previous size, just clamp to current size
						m.ClampWindowsToView()
					}
				}
			} else {
				m.LogInfo("[RESIZE] Restored session, same size (%dx%d), preserving layout", msg.Width, msg.Height)
			}

			// Flush PTY buffers for restored session resize
			m.FlushPTYBuffersAfterResize()

			// Clear and re-place kitty/sixel images after restore resize
			if m.KittyPassthrough != nil {
				m.KittyPassthrough.HideAllPlacements()
			}

			return m, nil
		}

		// Retile windows if in tiling mode
		if m.AutoTiling {
			m.TileAllWindows()
		} else if msg.Width < oldWidth || msg.Height < oldHeight {
			// Terminal got smaller in floating mode - clamp windows back into view
			m.ClampWindowsToView()
		}

		// Flush PTY buffers after resize to ensure TUI apps (btop, vim, etc.)
		// redraw properly. The PTY resize sends SIGWINCH, but we also need to
		// mark all content dirty and invalidate caches for the new output.
		m.FlushPTYBuffersAfterResize()

		// Clear and re-place all kitty/sixel images after resize.
		// During resize, window positions change which invalidates image positions.
		if m.KittyPassthrough != nil {
			m.KittyPassthrough.HideAllPlacements()
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

	// Multi-client daemon messages
	case StateSyncMsg:
		// Another client updated state - apply incrementally
		if msg.State != nil {
			// Track what changed for notifications
			oldMode := m.Mode
			oldWindowCount := len(m.Windows)
			oldWorkspace := m.CurrentWorkspace

			if err := m.ApplyStateSync(msg.State); err != nil {
				m.LogError("Failed to apply state sync: %v", err)
			} else {
				// Show notifications for significant changes
				newMode := m.Mode
				newWindowCount := len(m.Windows)
				newWorkspace := m.CurrentWorkspace

				// Mode change notification
				if oldMode != newMode {
					if newMode == TerminalMode {
						m.ShowNotification("Switched to Terminal mode", "info", 2*time.Second)
					} else {
						m.ShowNotification("Switched to Window mode", "info", 2*time.Second)
					}
				}

				// Window count change notification
				if newWindowCount > oldWindowCount {
					m.ShowNotification(fmt.Sprintf("Window created (%d total)", newWindowCount), "info", 2*time.Second)
				} else if newWindowCount < oldWindowCount {
					m.ShowNotification(fmt.Sprintf("Window closed (%d remaining)", newWindowCount), "info", 2*time.Second)
				}

				// Workspace change notification
				if oldWorkspace != newWorkspace {
					m.ShowNotification(fmt.Sprintf("Switched to workspace %d", newWorkspace), "info", 2*time.Second)
				}
			}
		}
		// Continue listening for more state syncs
		return m, ListenForStateSync(m.StateSyncChan)

	case ClientJoinedMsg:
		// Another client joined the session
		m.ShowNotification(fmt.Sprintf("Client joined (%d connected)", msg.ClientCount), "info", 2*time.Second)
		// Continue listening for more client events
		return m, ListenForClientEvents(m.ClientEventChan)

	case ClientLeftMsg:
		// Another client left the session
		m.ShowNotification(fmt.Sprintf("Client left (%d connected)", msg.ClientCount), "info", 2*time.Second)
		// Continue listening for more client events
		return m, ListenForClientEvents(m.ClientEventChan)

	case SessionResizeMsg:
		// Effective session size changed (min of all clients)
		// Set the effective size - GetRenderWidth/Height will use min(terminal, effective)
		if m.EffectiveWidth != msg.Width || m.EffectiveHeight != msg.Height {
			m.EffectiveWidth = msg.Width
			m.EffectiveHeight = msg.Height
			m.MarkAllDirty()
			// Retile if the effective render size changed
			if m.AutoTiling {
				m.TileAllWindows()
			}
			// CRITICAL: Force sync all daemon PTY dimensions after tiling
			// This ensures PTYs match the new window dimensions even if no animation was created
			// (e.g., when window was already at target position but PTY had stale dimensions)
			m.SyncDaemonPTYDimensions()
			m.ShowNotification(fmt.Sprintf("Session size: %dx%d (%d clients)", msg.Width, msg.Height, msg.ClientCount), "info", 2*time.Second)
		}
		return m, nil

	case ForceRefreshMsg:
		// Force re-render
		m.MarkAllDirty()
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

	case RemoteCommandMsg:
		// Execute remote command from CLI
		var err error
		var cmd tea.Cmd
		var notificationMsg string
		var resultData map[string]any // Rich data to return

		switch msg.CommandType {
		case "tape_command":
			// Show what command is being run
			if len(msg.TapeArgs) > 0 {
				notificationMsg = fmt.Sprintf("Remote: %s %s", msg.TapeCommand, msg.TapeArgs[0])
			} else {
				notificationMsg = fmt.Sprintf("Remote: %s", msg.TapeCommand)
			}

			// Handle query/inspection commands first (these are read-only, no side effects)
			switch msg.TapeCommand {
			case "ListWindows":
				// Return list of all windows (read-only, no notification)
				resultData = m.GetWindowListData()
				// Send result directly and return early to avoid side effects
				if m.DaemonClient != nil && msg.RequestID != "" {
					_ = m.DaemonClient.SendCommandResultWithData(msg.RequestID, true, "command executed", resultData)
				}
				return m, nil
			case "GetSessionInfo":
				// Return session information (read-only, no notification)
				resultData = m.GetSessionInfoData()
				if m.DaemonClient != nil && msg.RequestID != "" {
					_ = m.DaemonClient.SendCommandResultWithData(msg.RequestID, true, "command executed", resultData)
				}
				return m, nil
			case "GetWindow":
				// Return info about a specific window (read-only, no notification)
				if len(msg.TapeArgs) > 0 {
					resultData, err = m.GetWindowData(msg.TapeArgs[0])
				} else {
					// Return focused window
					resultData, err = m.GetFocusedWindowData()
				}
				if m.DaemonClient != nil && msg.RequestID != "" {
					if err != nil {
						_ = m.DaemonClient.SendCommandResult(msg.RequestID, false, err.Error())
					} else {
						_ = m.DaemonClient.SendCommandResultWithData(msg.RequestID, true, "command executed", resultData)
					}
				}
				return m, nil
			default:
				// Handle tape commands that return data specially
				switch tape.CommandType(msg.TapeCommand) {
				case tape.CommandTypeNewWindow:
					// Create window and capture ID
					name := ""
					if len(msg.TapeArgs) > 0 {
						name = msg.TapeArgs[0]
					}
					windowID, displayName, createErr := m.CreateNewWindowReturningID(name)
					if createErr != nil {
						err = createErr
					} else {
						resultData = map[string]any{
							"window_id": windowID,
							"name":      displayName,
						}
					}
				default:
					// Execute normally for other commands
					tapeCmd := &tape.Command{
						Type: tape.CommandType(msg.TapeCommand),
						Args: msg.TapeArgs,
					}
					executor := tape.NewCommandExecutor(m)
					err = executor.Execute(tapeCmd)
				}
			}
			// Retile if in tiling mode after command execution
			if m.AutoTiling {
				m.TileAllWindows()
			}
		case "send_keys":
			// Show what keys are being sent
			notificationMsg = fmt.Sprintf("Remote: send-keys %s", msg.Keys)

			// Parse keys and start sequential processing
			cmd, err = m.startRemoteSendKeys(msg.Keys, msg.Literal, msg.Raw, msg.RequestID)
			if err == nil {
				// Keys will be processed sequentially via RemoteKeyMsg
				// Show notification now, result will be sent after all keys processed
				m.ShowNotification(notificationMsg, "info", config.NotificationDuration)
				return m, cmd
			}
		case "set_config":
			// Show what config is being changed
			notificationMsg = fmt.Sprintf("Remote: set %s=%s", msg.ConfigPath, msg.ConfigValue)

			err = m.SetConfig(msg.ConfigPath, msg.ConfigValue)
			// Retile if in tiling mode after config change
			if m.AutoTiling {
				m.TileAllWindows()
			}
		case "tape_script":
			// Execute a full tape script
			notificationMsg = "Remote: executing tape script"

			// Parse and execute the tape script
			cmd, err = m.executeTapeScript(msg.TapeScript, msg.RequestID)
			if err == nil {
				// Script will be processed via RemoteTapeCommandMsg
				m.ShowNotification(notificationMsg, "info", config.NotificationDuration)
				return m, cmd
			}
		default:
			err = fmt.Errorf("unknown remote command type: %s", msg.CommandType)
		}

		m.MarkAllDirty()

		// Show notification for the remote command
		if err != nil {
			m.ShowNotification(fmt.Sprintf("Remote error: %v", err), "error", config.NotificationDuration)
		} else if notificationMsg != "" {
			m.ShowNotification(notificationMsg, "info", config.NotificationDuration)
		}

		// Send result back if we have a daemon client
		if m.DaemonClient != nil && msg.RequestID != "" {
			if err != nil {
				_ = m.DaemonClient.SendCommandResult(msg.RequestID, false, err.Error())
			} else {
				_ = m.DaemonClient.SendCommandResultWithData(msg.RequestID, true, "command executed", resultData)
			}
		}

		return m, cmd

	case RemoteKeyMsg:
		// Process a single key from a remote send-keys command
		var cmd tea.Cmd

		// Process this key through the input handler
		if inputHandler != nil {
			newModel, keyCmd := inputHandler(msg.Key, m)
			if newOS, ok := newModel.(*OS); ok {
				m = newOS
			}
			cmd = keyCmd
		}

		// If there are more keys, schedule the next one
		if len(msg.RemainingKeys) > 0 {
			nextKey := msg.RemainingKeys[0]
			remaining := msg.RemainingKeys[1:]
			nextCmd := func() tea.Msg {
				return RemoteKeyMsg{
					Key:           nextKey,
					RemainingKeys: remaining,
					RequestID:     msg.RequestID,
				}
			}
			// Use Sequence to ensure keys are processed in order, not concurrently
			if cmd != nil {
				return m, tea.Sequence(cmd, nextCmd)
			}
			return m, nextCmd
		}

		// Last key - schedule cleanup
		doneCmd := func() tea.Msg {
			return RemoteKeysDoneMsg{RequestID: msg.RequestID}
		}
		if cmd != nil {
			return m, tea.Sequence(cmd, doneCmd)
		}
		return m, doneCmd

	case RemoteKeysDoneMsg:
		// All remote keys have been processed - do final cleanup
		// Re-enable animations
		m.ProcessingRemoteKeys = false
		config.AnimationsSuppressed = false

		if m.AutoTiling {
			// Clear the BSP tree for current workspace to force a full rebuild
			// This ensures consistent state after multiple rapid operations
			if m.WorkspaceTrees != nil {
				m.WorkspaceTrees[m.CurrentWorkspace] = nil
			}
			m.TileAllWindows()
		}
		m.MarkAllDirty()

		// Send result back
		if m.DaemonClient != nil && msg.RequestID != "" {
			_ = m.DaemonClient.SendCommandResult(msg.RequestID, true, "keys sent")
		}

		return m, nil

	case RemoteTapeCommandMsg:
		// Process a single tape command from a remote script

		// Update progress tracking for display
		m.RemoteScriptIndex = msg.CommandIndex
		m.RemoteScriptTotal = msg.TotalCommands

		// Handle Sleep commands specially - they just wait
		if msg.Command.Type == tape.CommandTypeSleep && msg.Command.Delay > 0 {
			// For remote execution, we use tea.Tick to wait
			nextIndex := msg.CommandIndex + 1
			waitCmd := tea.Tick(msg.Command.Delay, func(t time.Time) tea.Msg {
				// After sleep, continue with remaining commands or done
				if len(msg.RemainingCommands) > 0 {
					nextCmd := msg.RemainingCommands[0]
					remaining := msg.RemainingCommands[1:]
					return RemoteTapeCommandMsg{
						Command:           nextCmd,
						RemainingCommands: remaining,
						RequestID:         msg.RequestID,
						CommandIndex:      nextIndex,
						TotalCommands:     msg.TotalCommands,
					}
				}
				return RemoteTapeScriptDoneMsg{RequestID: msg.RequestID}
			})
			return m, waitCmd
		}

		// Execute the tape command
		executor := tape.NewCommandExecutor(m)
		if err := executor.Execute(&msg.Command); err != nil {
			// Log error but continue with remaining commands
			m.ShowNotification(fmt.Sprintf("Script error: %v", err), "error", config.NotificationDuration)
		}

		// Retile if in tiling mode after command execution
		if m.AutoTiling {
			m.TileAllWindows()
		}

		// If there are more commands, schedule the next one with a delay
		// The delay allows the UI to render the current command's effects before moving on
		if len(msg.RemainingCommands) > 0 {
			nextCmd := msg.RemainingCommands[0]
			remaining := msg.RemainingCommands[1:]
			nextIndex := msg.CommandIndex + 1
			// Use tea.Tick with a delay to allow rendering to catch up
			// 50ms gives enough time for window creation and basic rendering
			nextCmdFunc := tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
				return RemoteTapeCommandMsg{
					Command:           nextCmd,
					RemainingCommands: remaining,
					RequestID:         msg.RequestID,
					CommandIndex:      nextIndex,
					TotalCommands:     msg.TotalCommands,
				}
			})
			return m, nextCmdFunc
		}

		// Last command - schedule cleanup with a delay for final render
		doneCmd := tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
			return RemoteTapeScriptDoneMsg{RequestID: msg.RequestID}
		})
		return m, doneCmd

	case RemoteTapeScriptDoneMsg:
		// All tape commands have been processed - do final cleanup
		// Re-enable animations
		m.ProcessingRemoteKeys = false
		config.AnimationsSuppressed = false

		// Mark script finish time for progress display
		m.ScriptFinishedTime = time.Now()

		// Update progress to show completion
		m.RemoteScriptIndex = m.RemoteScriptTotal

		if m.AutoTiling {
			// Clear the BSP tree for current workspace to force a full rebuild
			if m.WorkspaceTrees != nil {
				m.WorkspaceTrees[m.CurrentWorkspace] = nil
			}
			m.TileAllWindows()
		}
		m.MarkAllDirty()

		// Send result back
		if m.DaemonClient != nil && msg.RequestID != "" {
			_ = m.DaemonClient.SendCommandResult(msg.RequestID, true, "script executed")
		}

		return m, nil

	}

	return m, nil
}
