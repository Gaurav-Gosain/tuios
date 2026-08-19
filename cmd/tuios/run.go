package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof" // registers /debug/pprof handlers on http.DefaultServeMux
	"os"
	"os/signal"
	"runtime"
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

// startPprofServer serves net/http/pprof on --pprof when that flag is set.
//
// Block/mutex profiling is sampled, not exhaustive: rate 1 samples every event
// and adds heavy overhead under load, which is not worth it for representative
// contention data. Output is not printed so it cannot corrupt the TUI on stdout.
//
// Every path that runs the TUI calls this, including the daemon-attached one.
// Profiling an attached client is the only way to see the compositor under a
// real multi-pane session, which is where the interesting contention lives.
func startPprofServer() {
	if pprofAddr == "" {
		return
	}
	runtime.SetBlockProfileRate(10000) // one sample per ~10us blocked
	runtime.SetMutexProfileFraction(100)
	go func() {
		srv := &http.Server{Addr: pprofAddr, ReadHeaderTimeout: 5 * time.Second}
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("pprof server error: %v", err)
		}
	}()
}

// debugLogEvent logs events to /tmp/tuios-events.log when TUIOS_DEBUG_INTERNAL=1.
// Only logs KeyPressMsg, MouseMotionMsg, and unknown events in TerminalMode
// to diagnose phantom keypresses (issue #78).
func debugLogEvent(osModel *app.OS, msg tea.Msg) {
	if os.Getenv("TUIOS_DEBUG_INTERNAL") != "1" {
		return
	}

	// Note: we intentionally don't check HasMouseMode() here because
	// accessing the VT emulator's modes map from this goroutine causes
	// unrecoverable concurrent map read/write panics.
	mouseMode := "unknown"

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

	// An open context menu highlights the row under the pointer, which it can
	// only do if it is told the pointer moved. This filter is a whitelist that
	// drops every motion event it does not recognise, so a hover handler added
	// anywhere downstream is dead until the event is allowed through here.
	if os.ContextMenuActive() {
		return msg
	}

	// Allow motion events while a floating overlay panel is being dragged.
	// Overlay drags don't set os.Dragging, so without this the motion events
	// that move the panel are filtered out and the drag never tracks.
	if os.OverlayDragActive() {
		return msg
	}

	// A workspace pill drag rides motion for the same reason a sidebar drag
	// does, and is whitelisted here for the same reason: the filter drops every
	// motion event it does not recognise, so the reorder would never see the
	// pointer leave the pill it was pressed on and every drag would arrive as a
	// plain click.
	if os.DockWorkspaceDragActive() {
		return msg
	}

	// A sidebar session drag rides motion the same way an overlay drag does,
	// and hover in the sidebar band needs motion to track the row under the
	// pointer. HoverActive keeps one more event flowing after the pointer
	// leaves the band, which is the event that clears the stale highlight.
	if os.SidebarDragActive() {
		return msg
	}
	if os.SidebarActive() {
		if mm, ok := msg.(tea.MouseMotionMsg); ok {
			mouse := mm.Mouse()
			if os.SidebarHoverActive || os.SidebarBandContains(mouse.X, mouse.Y) {
				return msg
			}
		}
	}

	// Allow motion events for scrollback browser drag-to-select
	if os.ShowScrollbackBrowser {
		return msg
	}

	// The dots window-control style draws three unlabelled discs and names them
	// by revealing their symbols under the pointer, so that style is dead
	// without its motion. Like the sidebar's, one more event is let through
	// after the pointer leaves the controls, and that is the event that clears
	// the reveal.
	if mm, ok := msg.(tea.MouseMotionMsg); ok && os.WindowButtonHoverActive() {
		return msg
	} else if ok && config.WindowButtonStyle == config.WindowButtonStyleDots && !config.HideWindowButtons {
		mouse := mm.Mouse()
		if os.WindowButtonContains(mouse.X, mouse.Y) {
			return msg
		}
	}

	if os.Mode == app.TerminalMode {
		focusedWindow := os.GetFocusedWindow()
		if focusedWindow != nil && focusedWindow.Terminal != nil {
			if focusedWindow.Terminal.HasMouseMode() {
				return msg
			}
		}
	}

	// Focus-follows-mouse moves pane focus from bare motion over a pane, and
	// every overlay menu highlights the row under the pointer as it moves. Both
	// live downstream of this whitelist, so both are dead unless their motion is
	// let through here. Without this, the opted-in focus-follows setting simply
	// does nothing and non-context menus never track the cursor.
	if config.FocusFollowsMouse || os.AnyOverlayOpen() {
		return msg
	}

	return nil
}

// loadAndApplyConfig loads the user config (falling back to defaults on error),
// applies the appearance globals as the baseline, then applies the CLI-flag
// overrides on top. Every run path bootstraps through here, so standalone,
// daemon (tuios new), and ssh all honor the same overrides instead of each
// wiring its own set and drifting apart.
func loadAndApplyConfig() *config.UserConfig {
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		log.Printf("Warning: Failed to load config, using defaults: %v", err)
		userConfig = config.DefaultConfig()
	}

	// Appearance globals are the baseline; CLI flags win. LoadUserConfig no longer
	// applies globals itself, so this must run before ApplyOverrides.
	config.ApplyAppearanceConfig(userConfig)

	config.ApplyOverrides(config.Overrides{
		ASCIIOnly:           asciiOnly,
		BorderStyle:         borderStyle,
		DockbarPosition:     dockbarPosition,
		HideWindowButtons:   hideWindowButtons,
		WindowButtonStyle:   windowButtonStyle,
		HideScrollbar:       hideScrollbar,
		WindowTitlePosition: windowTitlePosition,
		HideClock:           hideClock,
		ShowClock:           showClock,
		ShowCPU:             showCPU,
		ShowRAM:             showRAM,
		SharedBorders:       sharedBorders,
		ZoomMaxWidth:        zoomMaxWidth,
		ScrollbackLines:     scrollbackLines,
		NoAnimations:        noAnimations,
		ConfirmQuit:         confirmQuit,
		ThemeName:           themeName,
	}, userConfig)

	return userConfig
}

func runLocal() error {
	if debugMode {
		_ = os.Setenv("TUIOS_DEBUG_INTERNAL", "1")
		fmt.Println("Debug mode enabled")
	}

	// The interactive TUI draws to this terminal. Go's standard log writes to
	// stderr, so the client/daemon [DEBUG] lines would share the screen with the
	// rendered UI and corrupt it. When internal debugging is on, divert the log
	// stream to a file so the screen stays clean; the external daemon subprocess
	// already discards its own output, so this covers the in-process client.
	if os.Getenv("TUIOS_DEBUG_INTERNAL") == "1" {
		if lf, lerr := os.OpenFile("/tmp/tuios-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); lerr == nil {
			log.SetOutput(lf)
		}
	}

	userConfig := loadAndApplyConfig()

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

	startPprofServer()

	app.SetInputHandler(input.HandleInput)

	keybindRegistry := config.NewKeybindRegistry(userConfig)

	if debugMode {
		configPath, _ := config.GetConfigPath()
		log.Printf("Configuration: %s", configPath)
	}

	isDaemonSession := os.Getenv("TUIOS_SESSION") != ""

	prw := app.NewPostRenderWriter(os.Stdout)

	initialOS := app.NewOS(app.OSOptions{
		KeybindRegistry:           keybindRegistry,
		UserConfig:                userConfig,
		ShowKeys:                  showKeys,
		IsDaemonSession:           isDaemonSession,
		EnableGraphicsPassthrough: true,
	})
	initialOS.PostRenderWriter = prw

	p := tea.NewProgram(
		initialOS,
		tea.WithFPS(config.MaxFPSCap),
		tea.WithoutSignalHandler(),
		tea.WithFilter(filterMouseMotion),
		tea.WithOutput(prw),
	)

	// Start config file watcher for hot-reload. The watcher goroutine only
	// parses the config; it must not apply the appearance globals directly
	// because the render loop reads them concurrently. Delivery goes through
	// p.Send so the apply happens on the Bubble Tea goroutine.
	if configPath, err := config.GetConfigPath(); err == nil {
		if watcher, err := config.NewWatcher(configPath, func(newConfig *config.UserConfig, err error) {
			if err != nil {
				log.Printf("Config reload error: %v", err)
				return
			}
			p.Send(app.ConfigReloadedMsg{Config: newConfig})
		}); err == nil {
			defer watcher.Stop()
		}
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		p.Send(tea.QuitMsg{})
	}()

	finalModel, err := p.Run()

	if finalOS, ok := finalModel.(*app.OS); ok {
		finalOS.DumpTickStats()
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
