package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func runAttach(sessionName string, createIfMissing bool) error {
	if !session.IsDaemonRunning() {
		if createIfMissing {
			fmt.Println("Starting TUIOS daemon...")
			if err := startDaemonBackground(); err != nil {
				return fmt.Errorf("failed to start daemon: %w", err)
			}
			time.Sleep(500 * time.Millisecond)
		} else {
			return fmt.Errorf("TUIOS daemon is not running. Use 'tuios new' to create a session")
		}
	}

	return runDaemonSession(sessionName, createIfMissing)
}

func runNewSession(sessionName string) error {
	if !session.IsDaemonRunning() {
		fmt.Println("Starting TUIOS daemon...")
		if err := startDaemonBackground(); err != nil {
			return fmt.Errorf("failed to start daemon: %w", err)
		}
		time.Sleep(500 * time.Millisecond)
	}

	if sessionName == "" {
		client := session.NewTUIClient()
		if err := client.Connect(version, 80, 24); err != nil {
			return fmt.Errorf("failed to connect to daemon: %w", err)
		}

		existingNames := client.AvailableSessionNames()
		_ = client.Close()

		sessionName = generateUniqueSessionName(existingNames)
		fmt.Printf("Creating session '%s'\n", sessionName)
	}

	return runDaemonSession(sessionName, true)
}

func generateUniqueSessionName(existingNames []string) string {
	existing := make(map[string]bool)
	for _, name := range existingNames {
		existing[name] = true
	}

	for i := 0; ; i++ {
		name := fmt.Sprintf("session-%d", i)
		if !existing[name] {
			return name
		}
	}
}

func runDaemonSession(sessionName string, createNew bool) error {
	if debugMode {
		_ = os.Setenv("TUIOS_DEBUG_INTERNAL", "1")
		fmt.Println("Debug mode enabled")
	}

	userConfig, err := config.LoadUserConfig()
	if err != nil {
		log.Printf("Warning: Failed to load config, using defaults: %v", err)
		userConfig = config.DefaultConfig()
	}

	config.ApplyOverrides(config.Overrides{
		ASCIIOnly:           asciiOnly,
		BorderStyle:         borderStyle,
		DockbarPosition:     dockbarPosition,
		HideWindowButtons:   hideWindowButtons,
		WindowTitlePosition: windowTitlePosition,
		HideClock:           hideClock,
		ScrollbackLines:     scrollbackLines,
		NoAnimations:        noAnimations,
		ThemeName:           themeName,
	}, userConfig)

	app.SetInputHandler(input.HandleInput)

	keybindRegistry := config.NewKeybindRegistry(userConfig)

	log.Printf("[CLIENT] Detecting terminal capabilities...")
	hostCaps := app.GetHostCapabilities()

	// Build client capabilities from detected host capabilities
	clientCaps := &session.ClientCapabilities{
		PixelWidth:    hostCaps.PixelWidth,
		PixelHeight:   hostCaps.PixelHeight,
		CellWidth:     hostCaps.CellWidth,
		CellHeight:    hostCaps.CellHeight,
		KittyGraphics: hostCaps.KittyGraphics,
		SixelGraphics: hostCaps.SixelGraphics,
		TerminalName:  hostCaps.TerminalName,
	}
	log.Printf("[CLIENT] Capabilities: cell=%dx%d, kitty=%v, sixel=%v, term=%s",
		clientCaps.CellWidth, clientCaps.CellHeight, clientCaps.KittyGraphics, clientCaps.SixelGraphics, clientCaps.TerminalName)

	log.Printf("[CLIENT] Connecting to daemon...")
	client := session.NewTUIClient()
	width, height := 80, 24

	if err := client.ConnectWithCapabilities(version, width, height, clientCaps); err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	log.Printf("[CLIENT] Connected to daemon")

	log.Printf("[CLIENT] Attaching to session '%s' (createNew=%v)", sessionName, createNew)
	state, err := client.AttachSession(sessionName, createNew, width, height)
	if err != nil {
		_ = client.Close()
		return fmt.Errorf("failed to attach to session: %w", err)
	}
	log.Printf("[CLIENT] Attached to session, got state")

	log.Printf("[CLIENT] Starting read loop")
	client.StartReadLoop()

	initialOS := app.NewOS(app.OSOptions{
		KeybindRegistry:           keybindRegistry,
		ShowKeys:                  showKeys,
		IsDaemonSession:           true,
		DaemonClient:              client,
		SessionName:               client.SessionName(),
		EnableGraphicsPassthrough: true,
	})

	windowCount := 0
	if state != nil {
		windowCount = len(state.Windows)
	}
	log.Printf("[CLIENT] State from daemon: %v, windows: %d", state != nil, windowCount)
	if state != nil && len(state.Windows) > 0 {
		log.Printf("[CLIENT] Restoring %d windows from session state", len(state.Windows))
		if err := initialOS.RestoreFromState(state); err != nil {
			log.Printf("Warning: Failed to restore session state: %v", err)
		}
		log.Printf("[CLIENT] RestoreFromState complete")

		log.Printf("[CLIENT] Restoring terminal states from daemon")
		if err := initialOS.RestoreTerminalStates(); err != nil {
			log.Printf("Warning: Failed to restore terminal states: %v", err)
		}

		log.Printf("[CLIENT] Setting up PTY output handlers")
		if err := initialOS.SetupPTYOutputHandlers(); err != nil {
			log.Printf("Warning: Failed to set up PTY handlers: %v", err)
		}

		// Sync daemon PTY dimensions to match window dimensions from state
		// This fixes the issue where PTYs have stale dimensions after detach/reattach
		log.Printf("[CLIENT] Syncing daemon PTY dimensions")
		initialOS.SyncDaemonPTYDimensions()

		log.Printf("[CLIENT] Restore complete, %d windows in OS", len(initialOS.Windows))
	} else {
		log.Printf("[CLIENT] No existing state to restore")
	}

	p := tea.NewProgram(
		initialOS,
		tea.WithFPS(config.NormalFPS),
		tea.WithoutSignalHandler(),
		tea.WithFilter(filterMouseMotion),
	)

	// Set up remote command handler for CLI-initiated commands
	// This handler sends messages to the Bubble Tea program which processes them in the main loop
	log.Printf("[CLIENT] Setting up remote command handler")
	client.OnRemoteCommand(func(payload *session.RemoteCommandPayload) error {
		// Send a Bubble Tea message to handle the command in the main loop
		// Use Send which is safe to call from any goroutine
		go func() {
			p.Send(app.RemoteCommandMsg{
				CommandType: payload.CommandType,
				TapeCommand: payload.TapeCommand,
				TapeArgs:    payload.TapeArgs,
				TapeScript:  payload.TapeScript,
				Keys:        payload.Keys,
				Literal:     payload.Literal,
				Raw:         payload.Raw,
				ConfigPath:  payload.ConfigPath,
				ConfigValue: payload.ConfigValue,
				RequestID:   payload.RequestID,
			})
		}()
		return nil // Don't report error here - it will be handled by the Update loop
	})

	// Set up multi-client handlers for state sync, join/leave notifications, and resize
	log.Printf("[CLIENT] Setting up multi-client handlers")

	// Handle state sync from other clients
	client.OnStateSync(func(state *session.SessionState, triggerType, sourceID string) {
		// Send a message to apply state in the main loop
		go func() {
			p.Send(app.StateSyncMsg{State: state, TriggerType: triggerType, SourceID: sourceID})
		}()
	})

	// Handle client join notifications
	client.OnClientJoined(func(clientID string, clientCount int, width, height int) {
		go func() {
			p.Send(app.ClientJoinedMsg{ClientID: clientID, ClientCount: clientCount, Width: width, Height: height})
		}()
	})

	// Handle client leave notifications
	client.OnClientLeft(func(clientID string, clientCount int) {
		go func() {
			p.Send(app.ClientLeftMsg{ClientID: clientID, ClientCount: clientCount})
		}()
	})

	// Handle session resize (min of all clients)
	client.OnSessionResize(func(width, height, clientCount int) {
		go func() {
			p.Send(app.SessionResizeMsg{Width: width, Height: height, ClientCount: clientCount})
		}()
	})

	// Handle force refresh
	client.OnForceRefresh(func(reason string) {
		go func() {
			p.Send(app.ForceRefreshMsg{Reason: reason})
		}()
	})

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		p.Send(tea.QuitMsg{})
	}()

	finalModel, err := p.Run()

	if finalOS, ok := finalModel.(*app.OS); ok {
		finalOS.SyncStateToDaemon()
		finalOS.Cleanup()
	}

	_ = client.Close()

	terminal.ResetTerminal()

	if err != nil {
		return fmt.Errorf("program error: %w", err)
	}

	fmt.Printf("[detached from session '%s']\n", sessionName)
	return nil
}

func runListSessions() error {
	if !session.IsDaemonRunning() {
		fmt.Println("TUIOS daemon is not running. No sessions available.")
		return nil
	}

	client := session.NewClient(&session.ClientConfig{
		Version: version,
	})

	if err := client.Connect(); err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	sessions, err := client.ListSessions()
	if err != nil {
		return err
	}

	if len(sessions) == 0 {
		fmt.Println("No sessions.")
		return nil
	}

	rows := make([][]string, 0, len(sessions))
	for _, s := range sessions {
		status := "detached"
		if s.Attached {
			status = "attached"
		}

		rows = append(rows, []string{
			s.Name,
			fmt.Sprintf("%d", s.WindowCount),
			status,
			formatTimeAgo(s.Created),
			formatTimeAgo(s.LastActive),
		})
	}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("8"))).
		Headers("NAME", "WINDOWS", "STATUS", "CREATED", "LAST ACTIVE").
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			baseStyle := lipgloss.NewStyle().Padding(0, 1)

			if row == table.HeaderRow {
				return baseStyle.Bold(true).Foreground(lipgloss.Color("12"))
			}

			switch col {
			case 0:
				return baseStyle.Foreground(lipgloss.Color("3")).Bold(true)
			case 2:
				if rows[row][col] == "attached" {
					return baseStyle.Foreground(lipgloss.Color("10"))
				}
				return baseStyle.Foreground(lipgloss.Color("8"))
			case 3, 4:
				return baseStyle.Foreground(lipgloss.Color("8"))
			default:
				return baseStyle
			}
		})

	fmt.Println(t.Render())
	fmt.Printf("\n%d session(s)\n", len(sessions))
	return nil
}

func formatTimeAgo(unixTime int64) string {
	if unixTime == 0 {
		return "-"
	}

	t := time.Unix(unixTime, 0)
	diff := time.Since(t)

	switch {
	case diff < time.Minute:
		return "just now"
	case diff < time.Hour:
		mins := int(diff.Minutes())
		if mins == 1 {
			return "1 min ago"
		}
		return fmt.Sprintf("%d mins ago", mins)
	case diff < 24*time.Hour:
		hours := int(diff.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	case diff < 7*24*time.Hour:
		days := int(diff.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	default:
		return t.Format("Jan 2, 2006")
	}
}

func runKillSession(sessionName string) error {
	if !session.IsDaemonRunning() {
		return fmt.Errorf("TUIOS daemon is not running")
	}

	client := session.NewClient(&session.ClientConfig{
		Version: version,
	})

	if err := client.Connect(); err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	if err := client.KillSession(sessionName); err != nil {
		return err
	}

	fmt.Printf("Killed session: %s\n", sessionName)
	return nil
}

func runDaemon(foreground bool) error {
	if session.IsDaemonRunning() {
		pid := session.GetDaemonPID()
		if pid > 0 {
			return fmt.Errorf("daemon already running (PID %d)", pid)
		}
		return fmt.Errorf("daemon already running")
	}

	if !foreground {
		return startDaemonBackground()
	}

	if session.GetDebugLevel() == session.DebugOff {
		userConfig, err := config.LoadUserConfig()
		if err == nil && userConfig.Daemon.LogLevel != "" {
			session.SetDebugLevel(session.ParseDebugLevel(userConfig.Daemon.LogLevel))
		}
	}

	daemon := session.NewDaemon(&session.DaemonConfig{
		Version: version,
	})

	return daemon.Run()
}

func runKillDaemon() error {
	if !session.IsDaemonRunning() {
		fmt.Println("TUIOS daemon is not running.")
		return nil
	}

	pid := session.GetDaemonPID()
	if pid > 0 {
		return killDaemonProcess(pid)
	}

	fmt.Println("Could not determine daemon PID. Try connecting to stop it.")
	return nil
}

// startDaemonBackground is defined in platform-specific files:
// - session_commands_unix.go for Unix/Linux/macOS
// - session_commands_windows.go for Windows
