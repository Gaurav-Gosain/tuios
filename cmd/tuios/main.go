// Package main implements TUIOS - Terminal UI Operating System.
// TUIOS is a terminal-based window manager that provides a modern interface
// for managing multiple terminal sessions with workspace support, tiling modes,
// and comprehensive keyboard/mouse interactions.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/server"
	tea "github.com/charmbracelet/bubbletea/v2"
)

// Version information (set by goreleaser)
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
	builtBy = "unknown"
)

// CLI flags
var (
	sshMode     = flag.Bool("ssh", false, "Run TUIOS as SSH server")
	sshPort     = flag.String("port", "2222", "SSH server port")
	sshHost     = flag.String("host", "localhost", "SSH server host")
	sshKeyPath  = flag.String("key-path", "", "Path to SSH host key (auto-generated if not specified)")
	showVersion = flag.Bool("version", false, "Show version information")
)

func main() {
	flag.Parse()

	if *showVersion {
		fmt.Printf("TUIOS %s\n", version)
		fmt.Printf("  commit: %s\n", commit)
		fmt.Printf("  built at: %s\n", date)
		fmt.Printf("  built by: %s\n", builtBy)
		os.Exit(0)
	}

	if *sshMode {
		// Run as SSH server
		runSSHServer()
	} else {
		// Run as local terminal application
		runLocal()
	}
}

func runLocal() {
	// Set up the input handler to break circular dependency
	app.SetInputHandler(input.HandleInput)

	// Start with no windows - user will create the first one
	initialOS := &app.OS{
		FocusedWindow:    -1,                    // No focused window initially
		WindowExitChan:   make(chan string, 10), // Buffer for window exit signals
		MouseSnapping:    false,                 // Disable mouse snapping by default
		CurrentWorkspace: 1,                     // Start on workspace 1
		NumWorkspaces:    9,                     // Support 9 workspaces (1-9)
		WorkspaceFocus:   make(map[int]int),     // Initialize workspace focus memory
	}

	// Initialize the Bubble Tea program with optimal settings
	p := tea.NewProgram(initialOS, tea.WithAltScreen(), tea.WithMouseAllMotion(), tea.WithFPS(config.NormalFPS))
	if _, err := p.Run(); err != nil {
		log.Printf("Fatal error: %v", err)
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runSSHServer() {
	// SSH server implementation will be added here
	log.Printf("Starting TUIOS SSH server on %s:%s", *sshHost, *sshPort)

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.Println("Shutting down SSH server...")
		cancel()
	}()

	// Start SSH server
	if err := server.StartSSHServer(ctx, *sshHost, *sshPort, *sshKeyPath); err != nil {
		log.Printf("SSH server error: %v", err)
		os.Exit(1)
	}
}
