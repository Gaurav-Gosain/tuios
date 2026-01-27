// Package main implements tuios-web - a web-based terminal server for TUIOS.
// This uses the sip library to serve TUIOS through the browser.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/sip"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"
)

// Version information (set by goreleaser)
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
	builtBy = "unknown"
)

// Command-line flags
var (
	webPort           string
	webHost           string
	webReadOnly       bool
	webMaxConnections int
	// TUIOS forwarded flags
	debugMode         bool
	asciiOnly         bool
	themeName         string
	borderStyle       string
	dockbarPosition   string
	hideWindowButtons bool
	scrollbackLines   int
	showKeys          bool
	noAnimations      bool
	// Daemon mode flags
	defaultSession string
	ephemeralMode  bool
)

// webServerConfig holds the server-wide configuration
var webServerConfig struct {
	defaultSession string
	ephemeral      bool
	version        string
}

func main() {
	rootCmd := &cobra.Command{
		Use:   "tuios-web",
		Short: "Web-based terminal server for TUIOS",
		Long: `tuios-web - Web Terminal Server for TUIOS

Serves TUIOS through the browser with full terminal emulation capabilities.
Powered by sip (github.com/Gaurav-Gosain/sip).

Server features:
  - Dual protocol support: WebTransport (HTTP/3 over QUIC) for low latency
    with automatic WebSocket fallback for broader compatibility
  - Self-signed TLS certificate generation for development
  - Configurable host, port, read-only mode, and connection limits
  - All TUIOS flags forwarded to spawned instances (theme, show-keys, etc.)
  - Structured logging with charmbracelet/log
  - Persistent sessions via daemon mode (default) with multi-client support

Client features:
  - WebGL-accelerated rendering via xterm.js for smooth 60fps output
  - Bundled JetBrains Mono Nerd Font for proper icon display
  - Settings panel for transport, renderer, and font size preferences
  - Cell-based mouse event deduplication reducing network traffic by 80-95%
  - requestAnimationFrame batching for efficient screen updates
  - Automatic reconnection with exponential backoff`,
		Example: `  # Start web server on default port (7681)
  tuios-web

  # Start on custom port
  tuios-web --port 8080

  # Bind to all interfaces for remote access
  tuios-web --host 0.0.0.0

  # Start with show-keys overlay
  tuios-web --show-keys

  # Start with a specific theme
  tuios-web --theme dracula

  # Start in read-only mode (view only)
  tuios-web --read-only

  # Limit concurrent connections
  tuios-web --max-connections 10

  # All clients share a single session
  tuios-web --default-session shared

  # Use ephemeral mode (no session persistence)
  tuios-web --ephemeral`,
		Version: version,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runWebServer()
		},
		SilenceUsage: true,
	}

	// Web server flags
	rootCmd.Flags().StringVar(&webPort, "port", "7681", "Web server port")
	rootCmd.Flags().StringVar(&webHost, "host", "localhost", "Web server host")
	rootCmd.Flags().BoolVar(&webReadOnly, "read-only", false, "Disable input from clients (view only)")
	rootCmd.Flags().IntVar(&webMaxConnections, "max-connections", 0, "Maximum concurrent connections (0 = unlimited)")

	// Daemon mode flags
	rootCmd.Flags().StringVar(&defaultSession, "default-session", "", "Default session name for all connections (creates shared session)")
	rootCmd.Flags().BoolVar(&ephemeralMode, "ephemeral", false, "Disable daemon mode (sessions don't persist)")

	// TUIOS forwarded flags
	rootCmd.Flags().BoolVar(&debugMode, "debug", false, "Enable debug logging")
	rootCmd.Flags().BoolVar(&asciiOnly, "ascii-only", false, "Use ASCII characters instead of Nerd Font icons")
	rootCmd.Flags().StringVar(&themeName, "theme", "", "Color theme to use (e.g., dracula, nord, tokyonight)")
	rootCmd.Flags().StringVar(&borderStyle, "border-style", "", "Window border style: rounded, normal, thick, double, hidden, block, ascii, outer-half-block, inner-half-block")
	rootCmd.Flags().StringVar(&dockbarPosition, "dockbar-position", "", "Dockbar position: bottom, top, hidden")
	rootCmd.Flags().BoolVar(&hideWindowButtons, "hide-window-buttons", false, "Hide window control buttons (minimize, maximize, close)")
	rootCmd.Flags().IntVar(&scrollbackLines, "scrollback-lines", 0, "Number of lines to keep in scrollback buffer (default: 10000, min: 100, max: 1000000)")
	rootCmd.Flags().BoolVar(&showKeys, "show-keys", false, "Enable showkeys overlay to display pressed keys")
	rootCmd.Flags().BoolVar(&noAnimations, "no-animations", false, "Disable UI animations for instant transitions")

	// Execute with fang
	if err := fang.Execute(
		context.Background(),
		rootCmd,
		fang.WithVersion(fmt.Sprintf("%s\nCommit: %s\nBuilt: %s\nBy: %s", version, commit, date, builtBy)),
	); err != nil {
		os.Exit(1)
	}
}

func runWebServer() error {
	// CRITICAL: Force lipgloss to use TrueColor BEFORE any styles are created.
	// By default, lipgloss detects color profile from os.Stdout, which isn't a TTY
	// when running as a web server. This causes all colors to be stripped.
	lipgloss.Writer.Profile = colorprofile.TrueColor

	// Set terminal environment variables
	_ = os.Setenv("TERM", "xterm-256color")
	_ = os.Setenv("COLORTERM", "truecolor")

	if debugMode {
		_ = os.Setenv("TUIOS_DEBUG_INTERNAL", "1")
	}

	// Store server config for handler
	webServerConfig.defaultSession = defaultSession
	webServerConfig.ephemeral = ephemeralMode
	webServerConfig.version = version

	// If using daemon mode, ensure daemon is running
	if !ephemeralMode {
		if err := session.EnsureDaemonRunning(); err != nil {
			log.Printf("Warning: Failed to start daemon, falling back to ephemeral mode: %v", err)
			webServerConfig.ephemeral = true
		}
	}

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.Println("Shutting down...")
		cancel()
		// Stop in-process daemon if we started one
		session.StopInProcessDaemon()

		// Force exit after short timeout or on second signal
		go func() {
			select {
			case <-c:
				os.Exit(0)
			case <-time.After(1 * time.Second):
				os.Exit(0)
			}
		}()
	}()

	// Apply global config options (CLI flags only, no user config at server level)
	config.ApplyOverrides(config.Overrides{
		ASCIIOnly:         asciiOnly,
		BorderStyle:       borderStyle,
		DockbarPosition:   dockbarPosition,
		HideWindowButtons: hideWindowButtons,
		ScrollbackLines:   scrollbackLines,
		NoAnimations:      noAnimations,
		ThemeName:         themeName,
	}, nil)

	// Create sip server
	sipConfig := sip.DefaultConfig()
	sipConfig.Host = webHost
	sipConfig.Port = webPort
	sipConfig.ReadOnly = webReadOnly
	sipConfig.MaxConnections = webMaxConnections
	sipConfig.Debug = debugMode

	server := sip.NewServer(sipConfig)

	// Log startup mode
	mode := "daemon"
	if webServerConfig.ephemeral {
		mode = "ephemeral"
	}
	log.Printf("Starting web server on %s:%s (mode: %s)", webHost, webPort, mode)

	// Serve TUIOS using sip
	return server.Serve(ctx, createTUIOSHandler)
}

// createTUIOSHandler creates a TUIOS instance for each web session.
func createTUIOSHandler(sess sip.Session) (tea.Model, []tea.ProgramOption) {
	pty := sess.Pty()

	// Determine session name
	sessionName := webServerConfig.defaultSession

	// If ephemeral mode or daemon not available, use old behavior
	if webServerConfig.ephemeral {
		return createEphemeralTUIOSInstance(pty.Width, pty.Height)
	}

	// Try to connect to daemon
	model, opts, err := createDaemonTUIOSInstance(sessionName, pty.Width, pty.Height)
	if err != nil {
		log.Printf("Warning: Failed to connect to daemon, using ephemeral mode: %v", err)
		return createEphemeralTUIOSInstance(pty.Width, pty.Height)
	}

	return model, opts
}

// createEphemeralTUIOSInstance creates a standalone TUIOS instance (old behavior)
func createEphemeralTUIOSInstance(width, height int) (tea.Model, []tea.ProgramOption) {
	// Load user configuration
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		userConfig = config.DefaultConfig()
	}

	// Set up the input handler
	app.SetInputHandler(input.HandleInput)

	// Create keybind registry
	keybindRegistry := config.NewKeybindRegistry(userConfig)

	// Create TUIOS instance
	tuiosInstance := app.NewOS(app.OSOptions{
		KeybindRegistry: keybindRegistry,
		ShowKeys:        showKeys,
		Width:           width,
		Height:          height,
	})

	return tuiosInstance, []tea.ProgramOption{
		tea.WithFPS(config.NormalFPS),
	}
}

// createDaemonTUIOSInstance creates a TUIOS instance connected to the daemon
func createDaemonTUIOSInstance(sessionName string, width, height int) (tea.Model, []tea.ProgramOption, error) {
	// Connect to daemon
	client := session.NewTUIClient()
	v := webServerConfig.version
	if v == "" {
		v = "web-client"
	}

	// Web mode doesn't have real terminal capabilities
	// Pass empty capabilities - graphics passthrough doesn't work in web mode anyway
	if err := client.ConnectWithCapabilities(v, width, height, nil); err != nil {
		return nil, nil, fmt.Errorf("failed to connect to daemon: %w", err)
	}

	// If no session name specified, get or create default
	if sessionName == "" {
		availableSessions := client.AvailableSessionNames()
		if len(availableSessions) == 0 {
			// No sessions exist, create a new one
			sessionName = "web-session"
		} else if len(availableSessions) == 1 {
			// Only one session, use it
			sessionName = availableSessions[0]
		} else {
			// Multiple sessions - use the first one
			// TODO: Could show session picker in web UI
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
		log.Printf("Warning: Failed to load config for web session, using defaults: %v", err)
		userConfig = config.DefaultConfig()
	}
	keybindRegistry := config.NewKeybindRegistry(userConfig)

	// Set up the input handler
	app.SetInputHandler(input.HandleInput)

	// Create TUIOS instance connected to daemon
	tuiosInstance := app.NewOS(app.OSOptions{
		KeybindRegistry:           keybindRegistry,
		ShowKeys:                  showKeys,
		Width:                     width,
		Height:                    height,
		IsDaemonSession:           true,
		DaemonClient:              client,
		SessionName:               sessionName,
		EnableGraphicsPassthrough: true,
	})

	// Restore state from daemon if available
	if state != nil && len(state.Windows) > 0 {
		log.Printf("[WEB] Restoring %d windows from session state", len(state.Windows))
		if err := tuiosInstance.RestoreFromState(state); err != nil {
			log.Printf("Warning: Failed to restore session state: %v", err)
		}

		// Restore terminal states
		if err := tuiosInstance.RestoreTerminalStates(); err != nil {
			log.Printf("Warning: Failed to restore terminal states: %v", err)
		}

		// Set up PTY output handlers for existing windows (workspace-aware)
		// This only subscribes to PTYs for windows in the current workspace
		if err := tuiosInstance.SetupPTYOutputHandlers(); err != nil {
			log.Printf("Warning: Failed to setup PTY handlers: %v", err)
		}

		// Sync daemon PTY dimensions to match window dimensions from state
		// This fixes the issue where PTYs have stale dimensions after detach/reattach
		tuiosInstance.SyncDaemonPTYDimensions()
	}

	// Register multi-client handlers
	registerMultiClientHandlers(tuiosInstance, client)

	return tuiosInstance, []tea.ProgramOption{
		tea.WithFPS(config.NormalFPS),
	}, nil
}

// registerMultiClientHandlers registers handlers for multi-client messages
func registerMultiClientHandlers(m *app.OS, client *session.TUIClient) {
	// Handle state sync from other clients via channel (thread-safe)
	client.OnStateSync(func(state *session.SessionState, triggerType, sourceID string) {
		log.Printf("[WEB] Received state sync: trigger=%s, source=%s", triggerType, sourceID[:8])
		// Send state to channel for processing in Bubble Tea event loop
		// This ensures thread-safe access to m.Windows
		if m.StateSyncChan != nil {
			select {
			case m.StateSyncChan <- state:
			default:
				log.Printf("[WEB] Warning: StateSyncChan full, dropping state sync")
			}
		}
	})

	// Handle client join notifications via channel (thread-safe)
	client.OnClientJoined(func(clientID string, clientCount int, width, height int) {
		log.Printf("[WEB] Client joined: %s (total: %d, size: %dx%d)", clientID[:8], clientCount, width, height)
		if m.ClientEventChan != nil {
			select {
			case m.ClientEventChan <- app.ClientEvent{Type: "joined", ClientID: clientID, ClientCount: clientCount, Width: width, Height: height}:
			default:
				log.Printf("[WEB] Warning: ClientEventChan full, dropping client joined event")
			}
		}
	})

	// Handle client leave notifications via channel (thread-safe)
	client.OnClientLeft(func(clientID string, clientCount int) {
		log.Printf("[WEB] Client left: %s (remaining: %d)", clientID[:8], clientCount)
		if m.ClientEventChan != nil {
			select {
			case m.ClientEventChan <- app.ClientEvent{Type: "left", ClientID: clientID, ClientCount: clientCount}:
			default:
				log.Printf("[WEB] Warning: ClientEventChan full, dropping client left event")
			}
		}
	})

	// Handle session resize (min of all clients)
	client.OnSessionResize(func(width, height, clientCount int) {
		log.Printf("[WEB] Session resize: %dx%d (clients: %d)", width, height, clientCount)
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
		log.Printf("[WEB] Force refresh requested: %s", reason)
		m.MarkAllDirty()
	})
}
