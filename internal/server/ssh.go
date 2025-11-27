// Package server provides SSH server functionality for TUIOS.
package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish/v2"
	"github.com/charmbracelet/wish/v2/bubbletea"
	"github.com/charmbracelet/wish/v2/logging"
)

// StartSSHServer initializes and runs the SSH server
func StartSSHServer(ctx context.Context, host, port, keyPath string) error {
	// Determine host key path
	var hostKeyPath string
	if keyPath != "" {
		hostKeyPath = keyPath
	} else {
		// Use default path in .ssh directory
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get user home directory: %w", err)
		}
		hostKeyPath = filepath.Join(homeDir, ".ssh", "tuios_host_key")
	}

	// Create SSH server with middleware
	server, err := wish.NewServer(
		wish.WithAddress(net.JoinHostPort(host, port)),
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
		log.Printf("Starting SSH server on %s", server.Addr)
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

// teaHandler creates a TUIOS instance for each SSH session
func teaHandler(session ssh.Session) (tea.Model, []tea.ProgramOption) {
	// Get PTY info from session
	pty, _, active := session.Pty()
	if !active {
		// No PTY requested, this shouldn't happen for TUIOS
		return nil, nil
	}

	// Load user configuration and create keybind registry
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		log.Printf("Warning: Failed to load config for SSH session, using defaults: %v", err)
		userConfig = config.DefaultConfig()
	}
	keybindRegistry := config.NewKeybindRegistry(userConfig)

	// Create a TUIOS instance for this session
	tuiosInstance := &app.OS{
		FocusedWindow:        -1,                               // No focused window initially
		WindowExitChan:       make(chan string, 10),            // Buffer for window exit signals
		MouseSnapping:        false,                            // Disable mouse snapping by default
		MasterRatio:          0.5,                              // Default 50/50 split for tiling
		CurrentWorkspace:     1,                                // Start on workspace 1
		NumWorkspaces:        9,                                // Support 9 workspaces (1-9)
		WorkspaceFocus:       make(map[int]int),                // Initialize workspace focus memory
		WorkspaceLayouts:     make(map[int][]app.WindowLayout), // Initialize layout storage
		WorkspaceHasCustom:   make(map[int]bool),               // Initialize custom layout tracker
		WorkspaceMasterRatio: make(map[int]float64),            // Initialize per-workspace master ratio
		PendingResizes:       make(map[string][2]int),          // Track pending PTY resizes
		Width:                pty.Window.Width,                 // Set initial width from SSH session
		Height:               pty.Window.Height,                // Set initial height from SSH session
		SSHSession:           session,                          // Store SSH session reference
		IsSSHMode:            true,                             // Flag to indicate SSH mode
		KeybindRegistry:      keybindRegistry,                  // User-configurable keybindings
	}

	// Return the model and program options
	// Note: AltScreen and MouseMode are now configured in View() method
	return tuiosInstance, []tea.ProgramOption{
		tea.WithFPS(config.NormalFPS),
	}
}
