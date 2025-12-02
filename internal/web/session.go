package web

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	tea "github.com/charmbracelet/bubbletea/v2"
)

// Session represents a terminal session running a TUIOS instance.
type Session struct {
	ID           string
	Program      *tea.Program
	InputWriter  *io.PipeWriter  // Write input to this to send to Bubble Tea
	OutputReader *io.PipeReader  // Read from this to get Bubble Tea's output
	Cols         int
	Rows         int
	cancelFunc   context.CancelFunc
	ctx          context.Context
	mu           sync.Mutex
	closed       bool
	startTime    time.Time
}

// Done returns a channel that is closed when the session ends.
func (s *Session) Done() <-chan struct{} {
	return s.ctx.Done()
}

// Resize changes the terminal dimensions.
func (s *Session) Resize(cols, rows int) {
	s.mu.Lock()
	s.Cols = cols
	s.Rows = rows
	s.mu.Unlock()
	
	// Send window size message to Bubble Tea
	if s.Program != nil {
		s.Program.Send(tea.WindowSizeMsg{Width: cols, Height: rows})
	}
}

func (s *Server) createSession(ctx context.Context) (*Session, error) {
	cols, rows := 80, 24

	logger.Debug("creating session", "cols", cols, "rows", rows)

	// Load user configuration and create keybind registry
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		logger.Warn("failed to load config, using defaults", "error", err)
		userConfig = config.DefaultConfig()
	}

	// Apply TUIOS args from config (theme, border style, etc.)
	applyTuiosArgs(s.config.TuiosArgs, userConfig)

	// Set up the input handler
	app.SetInputHandler(input.HandleInput)

	// Create keybind registry
	keybindRegistry := config.NewKeybindRegistry(userConfig)

	// Create a TUIOS instance for this web session
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
		Width:                cols,                             // Set initial width
		Height:               rows,                             // Set initial height
		KeybindRegistry:      keybindRegistry,                  // User-configurable keybindings
		RecentKeys:           []app.KeyEvent{},                 // Initialize empty key history
		KeyHistoryMaxSize:    5,                                // Default: show last 5 keys
		IsSSHMode:            false,                            // Not SSH mode
	}

	// Create pipes for Bubble Tea I/O
	// Input: we write to inputWriter, Bubble Tea reads from inputReader
	// Output: Bubble Tea writes to outputWriter, we read from outputReader
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()

	// Create the Bubble Tea program with pipe I/O
	program := tea.NewProgram(
		tuiosInstance,
		tea.WithFPS(config.NormalFPS),
		tea.WithInput(inputReader),
		tea.WithOutput(outputWriter),
	)

	sessionCtx, cancel := context.WithCancel(ctx)
	session := &Session{
		ID:           fmt.Sprintf("%d", time.Now().UnixNano()),
		Program:      program,
		InputWriter:  inputWriter,
		OutputReader: outputReader,
		Cols:         cols,
		Rows:         rows,
		cancelFunc:   cancel,
		ctx:          sessionCtx,
		startTime:    time.Now(),
	}

	// Start the Bubble Tea program in a goroutine
	go func() {
		logger.Debug("starting TUIOS program", "session", session.ID)
		if _, err := program.Run(); err != nil {
			logger.Error("TUIOS program error", "session", session.ID, "error", err)
		}
		logger.Debug("TUIOS program exited", "session", session.ID)
		// Close pipes when program exits
		_ = inputReader.Close()
		_ = outputWriter.Close()
		cancel()
	}()

	s.sessions.Store(session.ID, session)

	logger.Debug("session created", "session", session.ID)

	return session, nil
}

func (s *Server) closeSession(session *Session) {
	session.mu.Lock()
	if session.closed {
		session.mu.Unlock()
		return
	}
	session.closed = true
	session.mu.Unlock()

	duration := time.Since(session.startTime)

	// Stop the Bubble Tea program
	if session.Program != nil {
		session.Program.Quit()
	}

	session.cancelFunc()

	// Close pipes
	if session.InputWriter != nil {
		_ = session.InputWriter.Close()
	}
	if session.OutputReader != nil {
		_ = session.OutputReader.Close()
	}

	s.sessions.Delete(session.ID)

	logger.Debug("session closed",
		"session", session.ID,
		"duration", duration.Round(time.Millisecond),
	)
}

// applyTuiosArgs parses and applies TUIOS CLI arguments to the configuration
func applyTuiosArgs(args []string, userConfig *config.UserConfig) {
	// Parse arguments and apply to global config
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--debug":
			// Debug mode already set via environment variable
		case "--ascii-only":
			config.UseASCIIOnly = true
		case "--theme":
			if i+1 < len(args) {
				// Theme initialization will be handled by the caller
				// We just note it here for completeness
				i++
			}
		case "--border-style":
			if i+1 < len(args) {
				config.BorderStyle = args[i+1]
				i++
			}
		case "--dockbar-position":
			if i+1 < len(args) {
				config.DockbarPosition = args[i+1]
				i++
			}
		case "--hide-window-buttons":
			config.HideWindowButtons = true
		case "--scrollback-lines":
			if i+1 < len(args) {
				var lines int
				if _, err := fmt.Sscanf(args[i+1], "%d", &lines); err == nil {
					if lines < 100 {
						lines = 100
					} else if lines > 1000000 {
						lines = 1000000
					}
					config.ScrollbackLines = lines
				}
				i++
			}
		case "--show-keys":
			// ShowKeys is handled per-session in OS struct
		}
	}

	// Apply config file settings as defaults if not overridden by CLI
	if config.BorderStyle == "" {
		config.BorderStyle = userConfig.Appearance.BorderStyle
	}
	if config.DockbarPosition == "" {
		config.DockbarPosition = userConfig.Appearance.DockbarPosition
	}
	if !config.HideWindowButtons {
		config.HideWindowButtons = userConfig.Appearance.HideWindowButtons
	}
	if config.ScrollbackLines == 0 {
		config.ScrollbackLines = userConfig.Appearance.ScrollbackLines
	}
	if config.LeaderKey == "" && userConfig.Keybindings.LeaderKey != "" {
		config.LeaderKey = userConfig.Keybindings.LeaderKey
	}
}
