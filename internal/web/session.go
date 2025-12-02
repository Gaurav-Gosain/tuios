package web

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	xpty "github.com/charmbracelet/x/xpty"
)

// Session represents a terminal session running a TUIOS instance.
type Session struct {
	ID         string
	Program    *tea.Program
	Model      *app.OS  // Direct reference to the TUIOS model
	Pty        xpty.Pty // PTY interface for terminal semantics
	PtyMaster  *os.File // PTY master - handlers read/write here (Unix only)
	PtySlave   *os.File // PTY slave - Bubble Tea uses this (Unix only)
	Cols       int
	Rows       int
	cancelFunc context.CancelFunc
	ctx        context.Context
	mu         sync.Mutex
	closed     bool
	startTime  time.Time
	started    chan struct{} // Signals when program has started
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

	// Resize the PTY
	if s.Pty != nil {
		_ = s.Pty.Resize(cols, rows)
	}

	// Update the model directly AND send the message
	if s.Model != nil {
		s.Model.Width = cols
		s.Model.Height = rows
	}

	if s.Program != nil {
		s.Program.Send(tea.WindowSizeMsg{Width: cols, Height: rows})
	}
}

// WaitForStart blocks until the program has started
func (s *Session) WaitForStart() {
	<-s.started
}

func (s *Server) createSession(ctx context.Context, initialCols, initialRows int) (*Session, error) {
	// Use provided dimensions (from browser) or defaults
	cols, rows := initialCols, initialRows
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}

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
		Width:                cols,
		Height:               rows,
		KeybindRegistry:      keybindRegistry,
		RecentKeys:           []app.KeyEvent{},
		KeyHistoryMaxSize:    5,
		IsSSHMode:            false,
	}

	// Create a PTY for proper terminal semantics
	// This gives Bubble Tea a real terminal-like file descriptor
	ptyInstance, err := xpty.NewPty(cols, rows)
	if err != nil {
		return nil, fmt.Errorf("failed to open PTY: %w", err)
	}

	// Get master/slave file descriptors (Unix only)
	var ptyMaster, ptySlave *os.File
	if runtime.GOOS != "windows" {
		unixPty, ok := ptyInstance.(*xpty.UnixPty)
		if !ok {
			_ = ptyInstance.Close()
			return nil, fmt.Errorf("expected UnixPty on %s", runtime.GOOS)
		}
		ptyMaster = unixPty.Master()
		ptySlave = unixPty.Slave()
	}

	// Environment setup
	envs := []string{
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
	}

	// Create the Bubble Tea program using the PTY slave
	// The PTY slave provides proper terminal semantics that Bubble Tea expects
	program := tea.NewProgram(
		tuiosInstance,
		tea.WithFPS(config.NormalFPS),
		tea.WithInput(ptySlave),
		tea.WithOutput(ptySlave),
		tea.WithColorProfile(colorprofile.Env(envs)),
		tea.WithEnvironment(envs),
		tea.WithWindowSize(cols, rows),
		tea.WithFilter(func(_ tea.Model, msg tea.Msg) tea.Msg {
			if _, ok := msg.(tea.SuspendMsg); ok {
				return tea.ResumeMsg{}
			}
			return msg
		}),
	)

	sessionCtx, cancel := context.WithCancel(ctx)
	started := make(chan struct{})

	session := &Session{
		ID:         fmt.Sprintf("%d", time.Now().UnixNano()),
		Program:    program,
		Model:      tuiosInstance,
		Pty:        ptyInstance,
		PtyMaster:  ptyMaster,
		PtySlave:   ptySlave,
		Cols:       cols,
		Rows:       rows,
		cancelFunc: cancel,
		ctx:        sessionCtx,
		startTime:  time.Now(),
		started:    started,
	}

	// Start the Bubble Tea program in a goroutine
	go func() {
		defer func() {
			_ = ptyInstance.Close()
			cancel()
		}()

		logger.Debug("starting TUIOS program", "session", session.ID, "cols", cols, "rows", rows)
		close(started)

		if _, err := program.Run(); err != nil {
			logger.Error("TUIOS program error", "session", session.ID, "error", err)
		}
		logger.Debug("TUIOS program exited", "session", session.ID)
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

	if session.Program != nil {
		session.Program.Quit()
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
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--debug":
			// Debug mode already set via environment variable
		case "--ascii-only":
			config.UseASCIIOnly = true
		case "--theme":
			if i+1 < len(args) {
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

	// Apply config file settings as defaults
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
