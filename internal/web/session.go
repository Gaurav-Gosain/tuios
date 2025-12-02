package web

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	tea "github.com/charmbracelet/bubbletea/v2"
	xpty "github.com/charmbracelet/x/xpty"
)

// Session represents a terminal session running a TUIOS instance.
type Session struct {
	ID         string
	Pty        xpty.Pty
	Program    *tea.Program
	Cols       int
	Rows       int
	cancelFunc context.CancelFunc
	ctx        context.Context
	mu         sync.Mutex
	closed     bool
	startTime  time.Time
}

// Done returns a channel that is closed when the session ends.
func (s *Session) Done() <-chan struct{} {
	return s.ctx.Done()
}

func (s *Server) createSession(ctx context.Context) (*Session, error) {
	cols, rows := 80, 24

	logger.Debug("creating PTY", "cols", cols, "rows", rows)

	pty, err := xpty.NewPty(cols, rows)
	if err != nil {
		return nil, fmt.Errorf("failed to create PTY: %w", err)
	}

	// Load user configuration and create keybind registry
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		logger.Warn("failed to load config, using defaults", "error", err)
		userConfig = config.DefaultConfig()
	}

	// Apply TUIOS args from config (theme, border style, etc.)
	// These should have been parsed from CLI flags in cmd/tuios-web/main.go
	// and stored in s.config.TuiosArgs - we'll need to parse them and apply
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

	// Create the Bubble Tea program with PTY I/O
	program := tea.NewProgram(
		tuiosInstance,
		tea.WithFPS(config.NormalFPS),
		tea.WithInput(pty),
		tea.WithOutput(pty),
	)

	sessionCtx, cancel := context.WithCancel(ctx)
	session := &Session{
		ID:         fmt.Sprintf("%d", time.Now().UnixNano()),
		Pty:        pty,
		Program:    program,
		Cols:       cols,
		Rows:       rows,
		cancelFunc: cancel,
		ctx:        sessionCtx,
		startTime:  time.Now(),
	}

	// Start the Bubble Tea program in a goroutine
	go func() {
		logger.Debug("starting TUIOS program", "session", session.ID)
		if _, err := program.Run(); err != nil {
			logger.Error("TUIOS program error", "session", session.ID, "error", err)
		}
		logger.Debug("TUIOS program exited", "session", session.ID)
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
		// Give the program a moment to exit gracefully
		time.Sleep(100 * time.Millisecond)
	}

	session.cancelFunc()

	if session.Pty != nil {
		_ = session.Pty.Close()
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
