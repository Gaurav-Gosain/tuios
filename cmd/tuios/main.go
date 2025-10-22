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
	"runtime/pprof"
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
	cpuProfile  = flag.String("cpuprofile", "", "Write CPU profile to file")
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

	// Start CPU profiling if requested
	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			log.Fatalf("Could not create CPU profile: %v", err)
		}
		defer f.Close()

		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatalf("Could not start CPU profile: %v", err)
		}
		defer pprof.StopCPUProfile()
	}

	if *sshMode {
		// Run as SSH server
		runSSHServer()
	} else {
		// Run as local terminal application
		runLocal()
	}
}

// filterMouseMotion filters out redundant mouse motion events to reduce CPU usage
// Only passes through mouse motion during drag/resize operations
func filterMouseMotion(model tea.Model, msg tea.Msg) tea.Msg {
	// Allow all non-motion events through
	if _, ok := msg.(tea.MouseMotionMsg); !ok {
		return msg
	}

	// Type assert to our OS model
	os, ok := model.(*app.OS)
	if !ok {
		return msg
	}

	// Allow motion events during active interactions
	if os.Dragging || os.Resizing {
		return msg
	}

	// Allow motion events during text selection
	if os.SelectionMode {
		focusedWindow := os.GetFocusedWindow()
		if focusedWindow != nil && focusedWindow.IsSelecting {
			return msg
		}
	}

	// Filter out motion events when not interacting
	return nil
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
	p := tea.NewProgram(
		initialOS,
		tea.WithAltScreen(),               // Use alternate screen buffer
		tea.WithMouseAllMotion(),          // Enable mouse tracking
		tea.WithFPS(config.NormalFPS),     // Set target FPS
		tea.WithoutSignalHandler(),        // We handle signals ourselves
		tea.WithFilter(filterMouseMotion), // Filter unnecessary mouse motion events
	)
	if _, err := p.Run(); err != nil {
		log.Printf("Fatal error: %v", err)
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runSSHServer() {
	// Set up the input handler to break circular dependency
	app.SetInputHandler(input.HandleInput)

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
