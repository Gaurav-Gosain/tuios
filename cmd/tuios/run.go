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
	config.ApplyAppearanceConfig(userConfig, &config.Global)

	config.ApplyOverrides(flagOverrides(), &config.Global)

	return userConfig
}

// flagOverrides collects every interface CLI flag into one Overrides value, so
// each entrypoint that layers flags over the config applies the same set. The
// SSH server hands it to StartSSHServer, which applies it after the appearance
// baseline; applying it here first would only be undone by that baseline.
func flagOverrides() config.Overrides {
	return config.Overrides{
		ASCIIOnly:            asciiOnly,
		BorderStyle:          borderStyle,
		DockbarPosition:      dockbarPosition,
		HideWindowButtons:    hideWindowButtons,
		WindowButtonStyle:    windowButtonStyle,
		WindowButtonPosition: windowButtonPosition,
		HideScrollbar:        hideScrollbar,
		WindowTitlePosition:  windowTitlePosition,
		HideClock:            hideClock,
		ShowClock:            showClock,
		ShowCPU:              showCPU,
		ShowRAM:              showRAM,
		SharedBorders:        sharedBorders,
		ZoomMaxWidth:         zoomMaxWidth,
		ScrollbackLines:      scrollbackLines,
		NoAnimations:         noAnimations,
		ConfirmQuit:          confirmQuit,
		ThemeName:            themeName,
	}
}

func runLocal() error {
	// The same check every other way into the TUI makes: a screen that cannot
	// host it is far harder to diagnose once the TUI has taken it.
	if err := checkTerminal(); err != nil {
		return err
	}

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

	// A user who always wants the daemon should not have to type "tuios attach"
	// every time. The decision sits here, after the config is loaded and before
	// anything has drawn, so the standalone path is never half-entered.
	//
	// It affects bare "tuios" only. Every subcommand already says which mode it
	// wants, and a session already running is a separate process this cannot
	// reach, so turning the setting on never disturbs one.
	if useDaemonByDefault(userConfig) {
		return runAttach("", true)
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
		Client:          app.ClientLocal,
		KeybindRegistry: keybindRegistry,
		UserConfig:      userConfig,
		ShowKeys:        showKeys,
		IsDaemonSession: isDaemonSession,
		// One writer for the terminal: frames, kitty and sixel sequences all
		// serialize on it. Left nil, the passthroughs open their own /dev/tty
		// and nothing can order their writes against a frame.
		GraphicsOutput: prw,
	})
	initialOS.PostRenderWriter = prw

	// The shared list, then the one option that is this transport's: the
	// writer every frame and every graphics sequence serialize on.
	p := tea.NewProgram(initialOS, append(app.ProgramOptions(), tea.WithOutput(prw))...)

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

// sshServerFlags is the `tuios ssh` command line, gathered so the runner reads
// as one thing rather than as seven positional arguments.
type sshServerFlags struct {
	host           string
	port           string
	keyPath        string
	defaultSession string
	authorizedKeys string
	ephemeral      bool
	noAuth         bool
}

func runSSHServer(f sshServerFlags) error {
	if debugMode {
		_ = os.Setenv("TUIOS_DEBUG_INTERNAL", "1")
		fmt.Println("Debug mode enabled")
	}

	// Decided before anything starts, and before the daemon is touched.
	// StartSSHServer makes the same call and refuses the same binds; this one
	// exists so the refusal can be answered with the flags this command has,
	// which is the same reason cmd/tuios-web decides TLS in the command rather
	// than leaving it to sip.
	if err := checkSSHAuth(os.Stderr, f); err != nil {
		return err
	}

	app.SetInputHandler(input.HandleInput)

	log.Printf("Starting TUIOS SSH server on %s:%s", f.host, f.port)
	if f.defaultSession != "" {
		log.Printf("Default session: %s", f.defaultSession)
	}
	if f.ephemeral {
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
		Host:               f.host,
		Port:               f.port,
		KeyPath:            f.keyPath,
		DefaultSession:     f.defaultSession,
		AuthorizedKeysPath: f.authorizedKeys,
		NoAuth:             f.noAuth,
		Version:            version,
		Ephemeral:          f.ephemeral,
		ShowKeys:           showKeys,
		// The full flag set, not a subset: `tuios ssh` registers the same
		// interface flags as every other run command, and the server applies
		// them over the appearance baseline it loads. Applying them here
		// instead used to happen before that baseline, which clobbered them.
		Overrides: flagOverrides(),
	}
	if err := server.StartSSHServer(ctx, cfg); err != nil {
		return fmt.Errorf("SSH server error: %w", err)
	}
	return nil
}

// useDaemonByDefault reports whether a bare "tuios" should attach to a
// daemon-backed session rather than run standalone.
//
// Both overrides exist because the setting lives in the config file and the
// thing it turns on is the daemon: a daemon that will not start would otherwise
// leave the user with no way to open a terminal except to edit the file that is
// causing it. The flag is for one run, the environment variable for a shell that
// has to keep working.
func useDaemonByDefault(cfg *config.UserConfig) bool {
	if standaloneMode || os.Getenv("TUIOS_NO_DAEMON") == "1" {
		return false
	}
	return cfg != nil && cfg.Startup.Daemon
}
