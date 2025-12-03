// Package main implements tuios-web - a web-based terminal server for TUIOS.
// This uses the sip library to serve TUIOS through the browser.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Gaurav-Gosain/sip"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/fang"
	"github.com/charmbracelet/lipgloss/v2"
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
)

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
  tuios-web --max-connections 10`,
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

	// TUIOS forwarded flags
	rootCmd.Flags().BoolVar(&debugMode, "debug", false, "Enable debug logging")
	rootCmd.Flags().BoolVar(&asciiOnly, "ascii-only", false, "Use ASCII characters instead of Nerd Font icons")
	rootCmd.Flags().StringVar(&themeName, "theme", "", "Color theme to use (e.g., dracula, nord, tokyonight)")
	rootCmd.Flags().StringVar(&borderStyle, "border-style", "", "Window border style: rounded, normal, thick, double, hidden, block, ascii, outer-half-block, inner-half-block")
	rootCmd.Flags().StringVar(&dockbarPosition, "dockbar-position", "", "Dockbar position: bottom, top, hidden")
	rootCmd.Flags().BoolVar(&hideWindowButtons, "hide-window-buttons", false, "Hide window control buttons (minimize, maximize, close)")
	rootCmd.Flags().IntVar(&scrollbackLines, "scrollback-lines", 0, "Number of lines to keep in scrollback buffer (default: 10000, min: 100, max: 1000000)")
	rootCmd.Flags().BoolVar(&showKeys, "show-keys", false, "Enable showkeys overlay to display pressed keys")

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

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		cancel()
	}()

	// Apply global config options
	applyGlobalConfig()

	// Create sip server
	sipConfig := sip.DefaultConfig()
	sipConfig.Host = webHost
	sipConfig.Port = webPort
	sipConfig.ReadOnly = webReadOnly
	sipConfig.MaxConnections = webMaxConnections
	sipConfig.Debug = debugMode

	server := sip.NewServer(sipConfig)

	// Serve TUIOS using sip
	return server.Serve(ctx, createTUIOSHandler)
}

// createTUIOSHandler creates a TUIOS instance for each web session.
func createTUIOSHandler(sess sip.Session) (tea.Model, []tea.ProgramOption) {
	pty := sess.Pty()

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
	tuiosInstance := &app.OS{
		FocusedWindow:        -1,
		WindowExitChan:       make(chan string, 10),
		MouseSnapping:        false,
		MasterRatio:          0.5,
		CurrentWorkspace:     1,
		NumWorkspaces:        9,
		WorkspaceFocus:       make(map[int]int),
		WorkspaceLayouts:     make(map[int][]app.WindowLayout),
		WorkspaceHasCustom:   make(map[int]bool),
		WorkspaceMasterRatio: make(map[int]float64),
		PendingResizes:       make(map[string][2]int),
		Width:                pty.Width,
		Height:               pty.Height,
		KeybindRegistry:      keybindRegistry,
		RecentKeys:           []app.KeyEvent{},
		KeyHistoryMaxSize:    5,
		IsSSHMode:            false,
		ShowKeys:             showKeys,
	}

	return tuiosInstance, []tea.ProgramOption{
		tea.WithFPS(config.NormalFPS),
	}
}

// applyGlobalConfig applies CLI flags to global configuration.
func applyGlobalConfig() {
	if asciiOnly {
		config.UseASCIIOnly = true
	}
	if borderStyle != "" {
		config.BorderStyle = borderStyle
	}
	if dockbarPosition != "" {
		config.DockbarPosition = dockbarPosition
	}
	if hideWindowButtons {
		config.HideWindowButtons = true
	}
	if scrollbackLines > 0 {
		if scrollbackLines < 100 {
			scrollbackLines = 100
		} else if scrollbackLines > 1000000 {
			scrollbackLines = 1000000
		}
		config.ScrollbackLines = scrollbackLines
	}
	if themeName != "" {
		if err := theme.Initialize(themeName); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to load theme '%s': %v\n", themeName, err)
		}
	}
}
