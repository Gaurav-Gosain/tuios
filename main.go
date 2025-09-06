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
	"time"

	tea "github.com/charmbracelet/bubbletea/v2"
)

// CLI flags
var (
	sshMode    = flag.Bool("ssh", false, "Run TUIOS as SSH server")
	sshPort    = flag.String("port", "2222", "SSH server port")
	sshHost    = flag.String("host", "localhost", "SSH server host")
	sshKeyPath = flag.String("key-path", "", "Path to SSH host key (auto-generated if not specified)")
)

func main() {
	flag.Parse()

	if *sshMode {
		// Run as SSH server
		runSSHServer()
	} else {
		// Run as local terminal application
		runLocal()
	}
}

func runLocal() {
	// Start with no windows - user will create the first one
	initialOS := &OS{
		FocusedWindow:    -1,                    // No focused window initially
		WindowExitChan:   make(chan string, 10), // Buffer for window exit signals
		MouseSnapping:    false,                 // Disable mouse snapping by default
		CurrentWorkspace: 1,                     // Start on workspace 1
		NumWorkspaces:    9,                     // Support 9 workspaces (1-9)
		WorkspaceFocus:   make(map[int]int),     // Initialize workspace focus memory
	}

	// Initialize the Bubble Tea program with optimal settings
	p := tea.NewProgram(initialOS, tea.WithAltScreen(), tea.WithMouseAllMotion(), tea.WithFPS(NormalFPS))
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
	if err := startSSHServer(ctx, *sshHost, *sshPort, *sshKeyPath); err != nil {
		log.Printf("SSH server error: %v", err)
		os.Exit(1)
	}
}

// tickerMsg represents a periodic tick event for updating the UI.
type tickerMsg time.Time

// windowExitMsg signals that a terminal window process has exited.
type windowExitMsg struct {
	windowID string
}

// Init initializes the TUIOS application and returns initial commands to run.
// It enables mouse tracking, starts the tick timer, and listens for window exits.
func (m *OS) Init() tea.Cmd {
	return tea.Batch(
		tea.EnableMouseAllMotion,
		tea.EnableBracketedPaste,     // Better paste handling for the main app
		tea.EnableGraphemeClustering, // Better Unicode support
		tea.EnableReportFocus,        // Track when terminal gains/loses focus
		tickCmd(),
		listenForWindowExits(m.WindowExitChan),
	)
}

// listenForWindowExits creates a command that listens for window process exits.
// It safely reads from the exit channel and converts exit signals to messages.
func listenForWindowExits(exitChan chan string) tea.Cmd {
	return func() tea.Msg {
		// Safe channel read with protection against closed channel
		windowID, ok := <-exitChan
		if !ok {
			// Channel closed, return nil to stop listening
			return nil
		}
		return windowExitMsg{windowID: windowID}
	}
}

// tickCmd creates a command that generates tick messages at 60 FPS.
// This drives the main update loop for animations and terminal content updates.
func tickCmd() tea.Cmd {
	return tea.Tick(time.Second/NormalFPS, func(t time.Time) tea.Msg {
		return tickerMsg(t)
	})
}

// slowTickCmd creates a command that generates tick messages at 30 FPS.
// Used during user interactions to improve responsiveness.
func slowTickCmd() tea.Cmd {
	return tea.Tick(time.Second/InteractionFPS, func(t time.Time) tea.Msg {
		return tickerMsg(t)
	})
}

// Update handles all incoming messages and updates the application state.
// It processes keyboard, mouse, and timer events, managing windows and UI updates.
func (m *OS) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickerMsg:
		// Update animations
		m.UpdateAnimations()

		// Update system info
		m.UpdateCPUHistory()

		// Adaptive polling - slower during interactions for better mouse responsiveness
		hasChanges := m.MarkTerminalsWithNewContent()

		// Check if we have active animations
		hasAnimations := m.HasActiveAnimations()

		// Determine tick rate based on interaction mode
		nextTick := tickCmd()
		if m.InteractionMode {
			nextTick = slowTickCmd() // 30 FPS during interactions
		}

		// Skip rendering if no changes, no animations, and not in interaction mode (frame skipping)
		if !hasChanges && !hasAnimations && !m.InteractionMode && len(m.Windows) > 0 {
			// Continue ticking but don't trigger render
			return m, nextTick
		}

		return m, nextTick

	case windowExitMsg:
		// Handle window exit - find and close the window
		for i := range m.Windows {
			if m.Windows[i].ID == msg.windowID {
				m.DeleteWindow(i)
				break
			}
		}
		// Ensure we're in window management mode if no windows remain
		if len(m.Windows) == 0 {
			m.Mode = WindowManagementMode
		}
		return m, listenForWindowExits(m.WindowExitChan)

	case tea.KeyPressMsg:
		return m.handleKeyPress(msg)

	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height
		m.MarkAllDirty()

		// Retile windows if in tiling mode
		if m.AutoTiling {
			m.TileAllWindows()
		}

		return m, nil

	case tea.MouseClickMsg:
		return m.handleMouseClick(msg)

	case tea.MouseMotionMsg:
		return m.handleMouseMotion(msg)

	case tea.MouseReleaseMsg:
		return m.handleMouseRelease(msg)

	case tea.MouseWheelMsg:
		return m.handleMouseWheel(msg)

	case tea.MouseMsg:
		// Catch-all for any other mouse events to prevent them from leaking
		return m, nil

	case tea.ClipboardMsg:
		// Store clipboard content and perform paste operation
		m.ClipboardContent = string(msg)
		m.handleClipboardPaste()
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
