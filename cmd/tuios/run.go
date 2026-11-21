package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/debuglog"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/localclient"
	"github.com/Gaurav-Gosain/tuios/internal/server"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func loadAndApplyConfig() (*config.UserConfig, *config.Settings) {
	return localclient.LoadConfig(os.Getenv, flagOverrides())
}

func flagOverrides() config.Overrides {
	return interfaceFlags.Overrides()
}

func runLocal() error {
	if err := checkTerminal(); err != nil {
		return err
	}

	if debugMode {
		debuglog.Enable()
		fmt.Println("Debug mode enabled")
	}

	if debuglog.Enabled() {
		if lf, lerr := debuglog.Open(debuglog.Path); lerr == nil {
			log.SetOutput(lf)
		}
	}

	userConfig, settings := loadAndApplyConfig()

	if useDaemonByDefault(userConfig) {
		name := userConfig.Startup.Session
		if name != "" {
			attachFocusWindow = userConfig.Startup.Window
		}
		err := runAttach(name, true)
		if !errors.Is(err, errDaemonUnreachable) {
			return err
		}
		fmt.Fprintln(os.Stderr, "The TUIOS daemon did not start. This session runs standalone.")
		fmt.Fprintln(os.Stderr, "Sessions in this window are not saved. Run 'tuios daemon' to see why it fails.")
	}

	if err := startCPUProfile(); err != nil {
		return err
	}
	defer stopCPUProfile()

	startPprofServer()

	keybindRegistry := config.NewKeybindRegistry(userConfig)

	if debugMode {
		configPath, _ := config.GetConfigPath()
		log.Printf("Configuration: %s", configPath)
	}

	isDaemonSession := os.Getenv("TUIOS_SESSION") != ""

	c := localclient.New(localclient.Stdio(), app.OSOptions{
		Client:          app.ClientLocal,
		KeybindRegistry: keybindRegistry,
		UserConfig:      userConfig,
		ShowKeys:        interfaceFlags.ShowKeys,
		Overrides:       flagOverrides(),
		Settings:        settings,
		IsDaemonSession: isDaemonSession,
	}, nil)

	runReturned, finish := armSignalQuit(c.Program)

	restoreTabs := withoutHardTabs()
	finalModel, err := c.Run()
	runReturned()
	restoreTabs()
	c.Close()

	if finalOS, ok := finalModel.(*app.OS); ok {
		finalOS.DumpTickStats()
		c.Finish(finalOS)
	}

	terminal.ResetTerminal()
	finish()

	if err != nil {
		return errfmt.Errorf("program error: %w", err)
	}

	return nil
}

type sshServerFlags struct {
	host             string
	port             string
	keyPath          string
	defaultSession   string
	authorizedKeys   string
	ephemeral        bool
	noAuth           bool
	handshakeTimeout *time.Duration
	idleTimeout      *time.Duration
	maxHandshakes    *int
	maxSessions      *int
}

func (f sshServerFlags) checkLimits() error {
	for _, d := range []struct {
		name string
		v    *time.Duration
	}{{"--handshake-timeout", f.handshakeTimeout}, {"--idle-timeout", f.idleTimeout}} {
		if d.v != nil && *d.v < 0 {
			return errfmt.Errorf("%s is negative. Give a duration such as 2m, or 0 to turn the limit off", d.name)
		}
	}
	for _, c := range []struct {
		name string
		v    *int
	}{{"--max-handshakes", f.maxHandshakes}, {"--max-sessions", f.maxSessions}} {
		if c.v != nil && *c.v < 0 {
			return errfmt.Errorf("%s is negative. Give a count, or 0 to turn the limit off", c.name)
		}
	}
	return nil
}

func runSSHServer(f sshServerFlags) error {
	if debugMode {
		debuglog.Enable()
		fmt.Println("Debug mode enabled")
	}

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
		session.StopInProcessDaemon()
	}()

	cfg := &server.SSHServerConfig{
		Host:               f.host,
		Port:               f.port,
		KeyPath:            f.keyPath,
		DefaultSession:     f.defaultSession,
		AuthorizedKeysPath: f.authorizedKeys,
		NoAuth:             f.noAuth,
		HandshakeTimeout:   f.handshakeTimeout,
		IdleTimeout:        f.idleTimeout,
		MaxHandshakes:      f.maxHandshakes,
		MaxSessions:        f.maxSessions,
		Version:            version,
		Ephemeral:          f.ephemeral,
		ShowKeys:           interfaceFlags.ShowKeys,
		Overrides:          flagOverrides(),
	}
	if err := server.StartSSHServer(ctx, cfg); err != nil {
		return errfmt.Errorf("SSH server error: %w", err)
	}
	return nil
}

func useDaemonByDefault(cfg *config.UserConfig) bool {
	if standaloneMode || os.Getenv("TUIOS_NO_DAEMON") == "1" {
		return false
	}
	return cfg != nil && (cfg.Startup.Daemon || cfg.Startup.Session != "")
}
