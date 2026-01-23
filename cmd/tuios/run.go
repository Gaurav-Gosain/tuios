package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime/pprof"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/server"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// filterMouseMotion filters out redundant mouse motion events to reduce CPU usage.
// Only passes through mouse motion during drag/resize operations.
func filterMouseMotion(model tea.Model, msg tea.Msg) tea.Msg {
	if _, ok := msg.(tea.MouseMotionMsg); !ok {
		return msg
	}

	os, ok := model.(*app.OS)
	if !ok {
		return msg
	}

	if os.Dragging || os.Resizing {
		return msg
	}

	if os.SelectionMode {
		focusedWindow := os.GetFocusedWindow()
		if focusedWindow != nil && focusedWindow.IsSelecting {
			return msg
		}
	}

	if os.Mode == app.TerminalMode {
		focusedWindow := os.GetFocusedWindow()
		if focusedWindow != nil && focusedWindow.IsAltScreen {
			return msg
		}
	}

	return nil
}

func runLocal() error {
	if debugMode {
		_ = os.Setenv("TUIOS_DEBUG_INTERNAL", "1")
		fmt.Println("Debug mode enabled")
	}

	if asciiOnly {
		config.UseASCIIOnly = true
	}

	userConfig, err := config.LoadUserConfig()
	if err != nil {
		log.Printf("Warning: Failed to load config, using defaults: %v", err)
		userConfig = config.DefaultConfig()
	}

	if borderStyle == "" {
		config.BorderStyle = userConfig.Appearance.BorderStyle
	} else {
		config.BorderStyle = borderStyle
	}

	if dockbarPosition == "" {
		config.DockbarPosition = userConfig.Appearance.DockbarPosition
	} else {
		config.DockbarPosition = dockbarPosition
	}

	config.HideWindowButtons = hideWindowButtons || userConfig.Appearance.HideWindowButtons

	if windowTitlePosition == "" {
		if userConfig.Appearance.WindowTitlePosition != "" {
			config.WindowTitlePosition = userConfig.Appearance.WindowTitlePosition
		}
	} else {
		config.WindowTitlePosition = windowTitlePosition
	}

	config.HideClock = hideClock || userConfig.Appearance.HideClock

	finalScrollbackLines := userConfig.Appearance.ScrollbackLines
	if scrollbackLines > 0 {
		if scrollbackLines < 100 {
			finalScrollbackLines = 100
		} else {
			finalScrollbackLines = min(scrollbackLines, 1000000)
		}
	}
	config.ScrollbackLines = finalScrollbackLines

	if userConfig.Keybindings.LeaderKey != "" {
		config.LeaderKey = userConfig.Keybindings.LeaderKey
	}

	if noAnimations {
		config.AnimationsEnabled = false
	}

	if cpuProfile != "" {
		f, err := os.Create(cpuProfile)
		if err != nil {
			return fmt.Errorf("could not create CPU profile: %w", err)
		}
		defer func() {
			if closeErr := f.Close(); closeErr != nil {
				log.Printf("Warning: failed to close CPU profile file: %v", closeErr)
			}
		}()

		if err := pprof.StartCPUProfile(f); err != nil {
			return fmt.Errorf("could not start CPU profile: %w", err)
		}
		defer pprof.StopCPUProfile()
	}

	app.SetInputHandler(input.HandleInput)

	keybindRegistry := config.NewKeybindRegistry(userConfig)

	if debugMode {
		configPath, _ := config.GetConfigPath()
		log.Printf("Configuration: %s", configPath)
	}

	if err := theme.Initialize(themeName); err != nil {
		log.Printf("Warning: Failed to load theme '%s': %v", themeName, err)
		log.Printf("Falling back to default theme")
	}

	isDaemonSession := os.Getenv("TUIOS_SESSION") != ""

	initialOS := &app.OS{
		FocusedWindow:        -1,
		WindowExitChan:       make(chan string, 10),
		StateSyncChan:        make(chan *session.SessionState, 10),
		ClientEventChan:      make(chan app.ClientEvent, 10),
		MouseSnapping:        false,
		MasterRatio:          0.5,
		CurrentWorkspace:     1,
		NumWorkspaces:        9,
		WorkspaceFocus:       make(map[int]int),
		WorkspaceLayouts:     make(map[int][]app.WindowLayout),
		WorkspaceHasCustom:   make(map[int]bool),
		WorkspaceMasterRatio: make(map[int]float64),
		PendingResizes:       make(map[string][2]int),
		KeybindRegistry:      keybindRegistry,
		ShowKeys:             showKeys,
		RecentKeys:           []app.KeyEvent{},
		KeyHistoryMaxSize:    5,
		IsDaemonSession:      isDaemonSession,
		KittyRenderer:        app.NewKittyRenderer(),
		KittyPassthrough:     app.NewKittyPassthrough(),
		SixelPassthrough:     app.NewSixelPassthrough(),
	}

	p := tea.NewProgram(
		initialOS,
		tea.WithFPS(config.NormalFPS),
		tea.WithoutSignalHandler(),
		tea.WithFilter(filterMouseMotion),
	)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		p.Send(tea.QuitMsg{})
	}()

	finalModel, err := p.Run()

	if finalOS, ok := finalModel.(*app.OS); ok {
		finalOS.Cleanup()
	}

	fmt.Print("\033c")
	fmt.Print("\033[?1000l")
	fmt.Print("\033[?1002l")
	fmt.Print("\033[?1003l")
	fmt.Print("\033[?1004l")
	fmt.Print("\033[?1006l")
	fmt.Print("\033[?25h")
	fmt.Print("\033[?47l")
	fmt.Print("\033[0m")
	fmt.Print("\r\n")
	_ = os.Stdout.Sync()

	if err != nil {
		return fmt.Errorf("program error: %w", err)
	}

	return nil
}

func runSSHServer(sshHost, sshPort, sshKeyPath, defaultSession string, ephemeral bool) error {
	if debugMode {
		_ = os.Setenv("TUIOS_DEBUG_INTERNAL", "1")
		fmt.Println("Debug mode enabled")
	}

	if asciiOnly {
		config.UseASCIIOnly = true
	}

	app.SetInputHandler(input.HandleInput)

	if err := theme.Initialize(themeName); err != nil {
		log.Printf("Warning: Failed to load theme '%s': %v", themeName, err)
		log.Printf("Falling back to default theme")
	}

	log.Printf("Starting TUIOS SSH server on %s:%s", sshHost, sshPort)
	if defaultSession != "" {
		log.Printf("Default session: %s", defaultSession)
	}
	if ephemeral {
		log.Printf("Running in ephemeral mode (no daemon)")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.Println("Shutting down SSH server...")
		cancel()
		// Stop in-process daemon if we started one
		session.StopInProcessDaemon()
	}()

	cfg := &server.SSHServerConfig{
		Host:           sshHost,
		Port:           sshPort,
		KeyPath:        sshKeyPath,
		DefaultSession: defaultSession,
		Version:        version,
		Ephemeral:      ephemeral,
	}
	if err := server.StartSSHServer(ctx, cfg); err != nil {
		return fmt.Errorf("SSH server error: %w", err)
	}
	return nil
}
