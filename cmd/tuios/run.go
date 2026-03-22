package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime/pprof"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/server"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// debugLogEvent logs events to /tmp/tuios-events.log when TUIOS_DEBUG_INTERNAL=1.
// Only logs KeyPressMsg, MouseMotionMsg, and unknown events in TerminalMode
// to diagnose phantom keypresses (issue #78).
func debugLogEvent(osModel *app.OS, msg tea.Msg) {
	if os.Getenv("TUIOS_DEBUG_INTERNAL") != "1" {
		return
	}

	// Determine focused window mouse mode context
	mouseMode := "none"
	if fw := osModel.GetFocusedWindow(); fw != nil && fw.Terminal != nil {
		if fw.Terminal.HasMouseMode() {
			mouseMode = "has_mouse"
		} else {
			mouseMode = "no_mouse"
		}
	}

	modeStr := "WinMgmt"
	if osModel.Mode == app.TerminalMode {
		modeStr = "Terminal"
	}

	var logLine string
	switch m := msg.(type) {
	case tea.KeyPressMsg:
		logLine = fmt.Sprintf("[%s] KEY mode=%s mouse=%s: key=%q code=%d mod=%d text=%q\n",
			time.Now().Format("15:04:05.000"), modeStr, mouseMode,
			m.String(), m.Code, m.Mod, m.Text)
	case tea.MouseMotionMsg:
		// Only log in TerminalMode to avoid flooding
		if osModel.Mode != app.TerminalMode {
			return
		}
		logLine = fmt.Sprintf("[%s] MOUSE_MOTION mode=%s mouse=%s: x=%d y=%d\n",
			time.Now().Format("15:04:05.000"), modeStr, mouseMode, m.X, m.Y)
	default:
		return
	}

	f, err := os.OpenFile("/tmp/tuios-events.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(logLine)
	_ = f.Close()
}

// filterMouseMotion filters out redundant mouse motion events to reduce CPU usage.
// Only passes through mouse motion during drag/resize operations.
func filterMouseMotion(model tea.Model, msg tea.Msg) tea.Msg {
	if _, ok := msg.(tea.MouseMotionMsg); !ok {
		// Debug: log non-motion events (KeyPressMsg) before they reach Update
		if osModel, ok := model.(*app.OS); ok {
			debugLogEvent(osModel, msg)
		}
		return msg
	}

	os, ok := model.(*app.OS)
	if !ok {
		return msg
	}

	// Debug: log motion events
	debugLogEvent(os, msg)

	if os.Dragging || os.Resizing {
		return msg
	}

	// Allow motion events for scrollback browser drag-to-select
	if os.ShowScrollbackBrowser {
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
		if focusedWindow != nil && focusedWindow.Terminal != nil && focusedWindow.Terminal.HasMouseMode() {
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
		ShowClock:           showClock,
		ShowCPU:             showCPU,
		ShowRAM:             showRAM,
		SharedBorders:       sharedBorders,
		ScrollbackLines:     scrollbackLines,
		NoAnimations:        noAnimations,
		ThemeName:           themeName,
	}, userConfig)

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

	isDaemonSession := os.Getenv("TUIOS_SESSION") != ""

	initialOS := app.NewOS(app.OSOptions{
		KeybindRegistry:           keybindRegistry,
		ShowKeys:                  showKeys,
		IsDaemonSession:           isDaemonSession,
		EnableGraphicsPassthrough: true,
	})

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

	terminal.ResetTerminal()

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

	config.ApplyOverrides(config.Overrides{
		ASCIIOnly: asciiOnly,
		ThemeName: themeName,
	}, nil)

	app.SetInputHandler(input.HandleInput)

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
