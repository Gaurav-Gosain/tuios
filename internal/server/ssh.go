// Package server provides SSH server functionality for TUIOS.
package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish/v2"
	"github.com/charmbracelet/wish/v2/bubbletea"
	"github.com/charmbracelet/wish/v2/logging"
)

// SSHServerConfig holds configuration for the SSH server.
type SSHServerConfig struct {
	Host           string
	Port           string
	KeyPath        string
	DefaultSession string // If set, all connections attach to this session
	Ephemeral      bool   // If true, don't use daemon (old behavior)
	Version        string // For daemon handshake
}

// sshServerContext holds the server-wide context for daemon mode
var sshServerConfig *SSHServerConfig

// StartSSHServer initializes and runs the SSH server
func StartSSHServer(ctx context.Context, cfg *SSHServerConfig) error {
	sshServerConfig = cfg

	// Determine host key path
	var hostKeyPath string
	if cfg.KeyPath != "" {
		hostKeyPath = cfg.KeyPath
	} else {
		// Use default path in .ssh directory
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get user home directory: %w", err)
		}
		hostKeyPath = filepath.Join(homeDir, ".ssh", "tuios_host_key")
	}

	// If using daemon mode, ensure daemon is running
	if !cfg.Ephemeral {
		if err := session.EnsureDaemonRunning(); err != nil {
			log.Printf("Warning: Failed to start daemon, falling back to ephemeral mode: %v", err)
			cfg.Ephemeral = true
		}
	}

	// Create SSH server with middleware
	server, err := wish.NewServer(
		wish.WithAddress(net.JoinHostPort(cfg.Host, cfg.Port)),
		wish.WithHostKeyPath(hostKeyPath),
		wish.WithMiddleware(
			// Bubble Tea middleware for interactive sessions
			bubbletea.Middleware(teaHandler),
			// Logging middleware for connection tracking
			logging.Middleware(),
		),
	)
	if err != nil {
		return fmt.Errorf("failed to create SSH server: %w", err)
	}

	// Start server
	go func() {
		mode := "daemon"
		if cfg.Ephemeral {
			mode = "ephemeral"
		}
		log.Printf("Starting SSH server on %s (mode: %s)", server.Addr, mode)
		if err := server.ListenAndServe(); err != nil {
			log.Printf("SSH server error: %v", err)
		}
	}()

	// Wait for context cancellation
	<-ctx.Done()

	// Shutdown server gracefully
	log.Println("Shutting down SSH server...")
	return server.Shutdown(ctx)
}

// StartSSHServerLegacy is the legacy function signature for backward compatibility
func StartSSHServerLegacy(ctx context.Context, host, port, keyPath string) error {
	return StartSSHServer(ctx, &SSHServerConfig{
		Host:      host,
		Port:      port,
		KeyPath:   keyPath,
		Ephemeral: true, // Legacy mode is ephemeral
	})
}

// teaHandler creates a TUIOS instance for each SSH session
func teaHandler(sshSession ssh.Session) (tea.Model, []tea.ProgramOption) {
	// Get PTY info from session
	pty, _, active := sshSession.Pty()
	if !active {
		// No PTY requested, this shouldn't happen for TUIOS
		return nil, nil
	}

	cfg := sshServerConfig
	if cfg == nil {
		cfg = &SSHServerConfig{Ephemeral: true}
	}

	// Determine session name from SSH context
	sessionName := determineSessionName(sshSession, cfg)

	// If ephemeral mode or daemon not available, use old behavior
	if cfg.Ephemeral {
		return createEphemeralTUIOSInstance(sshSession, pty.Window.Width, pty.Window.Height)
	}

	// Try to connect to daemon
	model, opts, err := createDaemonTUIOSInstance(sshSession, sessionName, pty.Window.Width, pty.Window.Height, cfg)
	if err != nil {
		log.Printf("Warning: Failed to connect to daemon, using ephemeral mode: %v", err)
		return createEphemeralTUIOSInstance(sshSession, pty.Window.Width, pty.Window.Height)
	}

	return model, opts
}

// determineSessionName determines which session to attach to based on SSH context
func determineSessionName(sshSession ssh.Session, cfg *SSHServerConfig) string {
	// Priority 1: Default session configured on server
	if cfg.DefaultSession != "" {
		return cfg.DefaultSession
	}

	// Priority 2: SSH username (if not generic)
	user := sshSession.User()
	if user != "" && user != "tuios" && user != "root" && user != "anonymous" {
		return user
	}

	// Priority 3: Parse command for "attach <session>" pattern
	cmd := sshSession.Command()
	if len(cmd) >= 2 && cmd[0] == "attach" {
		return cmd[1]
	}

	// Priority 4: Empty string = show session picker or use default
	return ""
}

// createEphemeralTUIOSInstance creates a standalone TUIOS instance (old behavior)
func createEphemeralTUIOSInstance(sshSession ssh.Session, width, height int) (tea.Model, []tea.ProgramOption) {
	// Load user configuration and create keybind registry
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		log.Printf("Warning: Failed to load config for SSH session, using defaults: %v", err)
		userConfig = config.DefaultConfig()
	}
	keybindRegistry := config.NewKeybindRegistry(userConfig)

	// Set up the input handler
	app.SetInputHandler(input.HandleInput)

	// Create a TUIOS instance for this session
	tuiosInstance := &app.OS{
		FocusedWindow:        -1,
		WindowExitChan:       make(chan string, 10),
		StateSyncChan:        make(chan *session.SessionState, 10),
		MouseSnapping:        false,
		MasterRatio:          0.5,
		CurrentWorkspace:     1,
		NumWorkspaces:        9,
		WorkspaceFocus:       make(map[int]int),
		WorkspaceLayouts:     make(map[int][]app.WindowLayout),
		WorkspaceHasCustom:   make(map[int]bool),
		WorkspaceMasterRatio: make(map[int]float64),
		PendingResizes:       make(map[string][2]int),
		Width:                width,
		Height:               height,
		SSHSession:           sshSession,
		IsSSHMode:            true,
		KeybindRegistry:      keybindRegistry,
		RecentKeys:           []app.KeyEvent{},
		KeyHistoryMaxSize:    5,
	}

	return tuiosInstance, []tea.ProgramOption{
		tea.WithFPS(config.NormalFPS),
	}
}

// createDaemonTUIOSInstance creates a TUIOS instance connected to the daemon
func createDaemonTUIOSInstance(sshSession ssh.Session, sessionName string, width, height int, cfg *SSHServerConfig) (tea.Model, []tea.ProgramOption, error) {
	// Connect to daemon
	client := session.NewTUIClient()
	version := cfg.Version
	if version == "" {
		version = "ssh-client"
	}

	if err := client.Connect(version, width, height); err != nil {
		return nil, nil, fmt.Errorf("failed to connect to daemon: %w", err)
	}

	// If no session name specified, show picker or get default
	if sessionName == "" {
		availableSessions := client.AvailableSessionNames()
		if len(availableSessions) == 0 {
			// No sessions exist, create a new one
			sessionName = "ssh-session"
		} else if len(availableSessions) == 1 {
			// Only one session, use it
			sessionName = availableSessions[0]
		} else {
			// Multiple sessions - use the first one for now
			// TODO: Could run session picker here, but that requires a different flow
			sessionName = availableSessions[0]
			log.Printf("Multiple sessions available, attaching to: %s", sessionName)
		}
	}

	// Attach to session (create if doesn't exist)
	state, err := client.AttachSession(sessionName, true, width, height)
	if err != nil {
		_ = client.Close()
		return nil, nil, fmt.Errorf("failed to attach to session: %w", err)
	}

	// Start read loop for daemon messages
	client.StartReadLoop()

	// Load user configuration
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		log.Printf("Warning: Failed to load config for SSH session, using defaults: %v", err)
		userConfig = config.DefaultConfig()
	}
	keybindRegistry := config.NewKeybindRegistry(userConfig)

	// Set up the input handler
	app.SetInputHandler(input.HandleInput)

	// Create TUIOS instance connected to daemon
	tuiosInstance := &app.OS{
		FocusedWindow:        -1,
		WindowExitChan:       make(chan string, 10),
		StateSyncChan:        make(chan *session.SessionState, 10),
		MouseSnapping:        false,
		MasterRatio:          0.5,
		CurrentWorkspace:     1,
		NumWorkspaces:        9,
		WorkspaceFocus:       make(map[int]int),
		WorkspaceLayouts:     make(map[int][]app.WindowLayout),
		WorkspaceHasCustom:   make(map[int]bool),
		WorkspaceMasterRatio: make(map[int]float64),
		PendingResizes:       make(map[string][2]int),
		Width:                width,
		Height:               height,
		SSHSession:           sshSession,
		IsSSHMode:            true,
		KeybindRegistry:      keybindRegistry,
		RecentKeys:           []app.KeyEvent{},
		KeyHistoryMaxSize:    5,
		IsDaemonSession:      true,
		DaemonClient:         client,
		SessionName:          sessionName,
		// Don't set EffectiveWidth/Height here - let the daemon broadcast
		// the effective size via SessionResizeMsg when there are multiple clients.
	}

	// Restore state from daemon if available
	if state != nil && len(state.Windows) > 0 {
		log.Printf("[SSH] Restoring %d windows from session state", len(state.Windows))
		if err := tuiosInstance.RestoreFromState(state); err != nil {
			log.Printf("Warning: Failed to restore session state: %v", err)
		}

		// Restore terminal states
		if err := tuiosInstance.RestoreTerminalStates(); err != nil {
			log.Printf("Warning: Failed to restore terminal states: %v", err)
		}

		// Set up PTY output handlers for existing windows
		for _, win := range tuiosInstance.Windows {
			setupPTYHandler(tuiosInstance, client, win)
		}
	}

	// Register multi-client handlers
	registerMultiClientHandlers(tuiosInstance, client)

	return tuiosInstance, []tea.ProgramOption{
		tea.WithFPS(config.NormalFPS),
	}, nil
}

// setupPTYHandler sets up input/output handling for a window's PTY
func setupPTYHandler(m *app.OS, client *session.TUIClient, win *terminal.Window) {
	if win.PTYID == "" {
		log.Printf("[SSH] setupPTYHandler: window %s has no PTYID, skipping", win.ID[:8])
		return
	}

	ptyID := win.PTYID
	log.Printf("[SSH] Setting up PTY handler for window %s, PTYID=%s", win.ID[:8], ptyID[:8])

	// CRITICAL: Set up the daemon write function for input
	// Without this, SendInput will fail and typing won't work
	win.DaemonWriteFunc = func(data []byte) error {
		return client.WritePTY(ptyID, data)
	}

	// Set up the daemon resize function
	win.DaemonResizeFunc = func(width, height int) error {
		return client.ResizePTY(ptyID, width, height)
	}

	// Start the response reader to handle DA queries and other terminal responses
	win.StartDaemonResponseReader()

	// Subscribe to PTY output
	err := client.SubscribePTY(ptyID, func(data []byte) {
		win.WriteOutputAsync(data)
	})
	if err != nil {
		log.Printf("[SSH] Failed to subscribe to PTY %s: %v", ptyID[:8], err)
	}

	// Handle PTY close
	windowID := win.ID
	client.OnPTYClosed(ptyID, func() {
		log.Printf("[SSH] PTY closed for window %s", windowID[:8])
		// Send a message to close the window through the window exit channel
		select {
		case m.WindowExitChan <- windowID:
		default:
		}
	})
}

// registerMultiClientHandlers registers handlers for multi-client messages
func registerMultiClientHandlers(m *app.OS, client *session.TUIClient) {
	// Handle state sync from other clients via channel (thread-safe)
	client.OnStateSync(func(state *session.SessionState, triggerType, sourceID string) {
		log.Printf("[SSH] Received state sync: trigger=%s, source=%s", triggerType, sourceID[:8])
		// Send state to channel for processing in Bubble Tea event loop
		// This ensures thread-safe access to m.Windows
		if m.StateSyncChan != nil {
			select {
			case m.StateSyncChan <- state:
			default:
				log.Printf("[SSH] Warning: StateSyncChan full, dropping state sync")
			}
		}
	})

	// Handle client join notifications
	client.OnClientJoined(func(clientID string, clientCount int, width, height int) {
		log.Printf("[SSH] Client joined: %s (total: %d, size: %dx%d)", clientID[:8], clientCount, width, height)
		m.ShowNotification(fmt.Sprintf("Client joined (%d connected)", clientCount), "info", 2000)
	})

	// Handle client leave notifications
	client.OnClientLeft(func(clientID string, clientCount int) {
		log.Printf("[SSH] Client left: %s (remaining: %d)", clientID[:8], clientCount)
		m.ShowNotification(fmt.Sprintf("Client left (%d connected)", clientCount), "info", 2000)
	})

	// Handle session resize (min of all clients)
	client.OnSessionResize(func(width, height, clientCount int) {
		log.Printf("[SSH] Session resize: %dx%d (clients: %d)", width, height, clientCount)
		// Update effective size to match the session size (min of all clients)
		// This ensures all clients see the same content
		if m.EffectiveWidth != width || m.EffectiveHeight != height {
			m.EffectiveWidth = width
			m.EffectiveHeight = height
			m.MarkAllDirty()
			if m.AutoTiling {
				m.TileAllWindows()
			}
		}
	})

	// Handle force refresh
	client.OnForceRefresh(func(reason string) {
		log.Printf("[SSH] Force refresh requested: %s", reason)
		m.MarkAllDirty()
	})
}

// Window is an alias for terminal.Window for use in this package
type Window = terminal.Window

// Helper function to truncate strings for logging
func truncateID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// parseSSHCommand parses SSH command arguments
func parseSSHCommand(cmd []string) (action string, args []string) {
	if len(cmd) == 0 {
		return "", nil
	}
	action = strings.ToLower(cmd[0])
	if len(cmd) > 1 {
		args = cmd[1:]
	}
	return action, args
}
