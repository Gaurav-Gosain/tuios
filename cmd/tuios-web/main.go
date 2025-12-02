// Package main implements tuios-web - a web-based terminal server for TUIOS.
// This is a separate binary to isolate the web server functionality from the main TUIOS binary
// for security and modularity reasons.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Gaurav-Gosain/tuios/internal/web"
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
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "tuios-web",
		Short: "Web-based terminal server for TUIOS",
		Long: `tuios-web - Web Terminal Server for TUIOS

Serves TUIOS through the browser with full terminal emulation capabilities.

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
  - Automatic reconnection with exponential backoff

This is a separate binary from the main TUIOS to isolate web server functionality
for security purposes, preventing the web server from being used as a backdoor.`,
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
	// Handle debug flag
	if debugMode {
		_ = os.Setenv("TUIOS_DEBUG_INTERNAL", "1")
	}

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		cancel()
	}()

	// Build TUIOS args from global flags
	var tuiosArgs []string
	if debugMode {
		tuiosArgs = append(tuiosArgs, "--debug")
	}
	if asciiOnly {
		tuiosArgs = append(tuiosArgs, "--ascii-only")
	}
	if themeName != "" {
		tuiosArgs = append(tuiosArgs, "--theme", themeName)
	}
	if borderStyle != "" {
		tuiosArgs = append(tuiosArgs, "--border-style", borderStyle)
	}
	if dockbarPosition != "" {
		tuiosArgs = append(tuiosArgs, "--dockbar-position", dockbarPosition)
	}
	if hideWindowButtons {
		tuiosArgs = append(tuiosArgs, "--hide-window-buttons")
	}
	if scrollbackLines > 0 {
		tuiosArgs = append(tuiosArgs, "--scrollback-lines", fmt.Sprintf("%d", scrollbackLines))
	}
	if showKeys {
		tuiosArgs = append(tuiosArgs, "--show-keys")
	}

	// Create and start web server
	config := web.DefaultConfig()
	config.Host = webHost
	config.Port = webPort
	config.ReadOnly = webReadOnly
	config.MaxConnections = webMaxConnections
	config.TuiosArgs = tuiosArgs
	config.Debug = debugMode

	server := web.NewServer(config)
	if err := server.Start(ctx); err != nil {
		return fmt.Errorf("web server error: %w", err)
	}
	return nil
}
