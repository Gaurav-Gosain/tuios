// Package main implements tuios-web - a web-based terminal server for TUIOS.
// This uses the sip library to serve TUIOS through the browser.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/sip"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"
)

// Version information (set by goreleaser)
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
	builtBy = "unknown"
)

// Command-line flags
var (
	webPort           string
	webHost           string
	webReadOnly       bool
	webMaxConnections int
	webTLSCert        string
	webTLSKey         string
	webAutoTLS        bool
	webInsecure       bool
	webTouch          string
	// TUIOS forwarded flags
	debugMode         bool
	asciiOnly         bool
	themeName         string
	borderStyle       string
	dockbarPosition   string
	hideWindowButtons bool
	scrollbackLines   int
	showKeys          bool
	noAnimations      bool
	// Daemon mode flags
	defaultSession string
	ephemeralMode  bool
)

// webServerConfig holds the server-wide configuration
var webServerConfig struct {
	defaultSession string
	ephemeral      bool
	version        string
}

func main() {
	rootCmd := &cobra.Command{
		Use:   "tuios-web",
		Short: "Web-based terminal server for TUIOS",
		Long: `tuios-web - Web Terminal Server for TUIOS

Serves TUIOS through the browser with full terminal emulation capabilities.
Powered by sip (github.com/Gaurav-Gosain/sip).

Server features:
  - Dual protocol support: WebTransport (HTTP/3 over QUIC) for low latency
    with automatic WebSocket fallback for broader compatibility
  - HTTPS from a self-signed certificate tuios-web generates and keeps
    (--auto-tls), or from your own (--cert/--key). A bind to a LAN address
    requires one of them unless you opt into clear text with --insecure
  - Configurable host, port, read-only mode, and connection limits
  - All TUIOS flags forwarded to spawned instances (theme, show-keys, etc.)
  - Structured logging with charmbracelet/log
  - Persistent sessions via daemon mode (default) with multi-client support

Client features:
  - WebGL-accelerated rendering via xterm.js for smooth 60fps output
  - Bundled JetBrains Mono Nerd Font for proper icon display
  - Settings panel for transport, renderer, and font size preferences
  - Cell-based mouse event deduplication reducing network traffic by 80-95%
  - requestAnimationFrame batching for efficient screen updates
  - Automatic reconnection with exponential backoff`,
		Example: `  # Start web server on default port (7681)
  tuios-web

  # Start on custom port
  tuios-web --port 8080

  # Reach the server from a phone on the same network, over TLS
  tuios-web --host 0.0.0.0 --auto-tls

  # Same, from a certificate you already have
  tuios-web --host 0.0.0.0 --cert cert.pem --key key.pem

  # Same, on a network you trust, with nothing encrypted
  tuios-web --host 0.0.0.0 --insecure

  # Start with show-keys overlay
  tuios-web --show-keys

  # Start with a specific theme
  tuios-web --theme dracula

  # Start in read-only mode (view only)
  tuios-web --read-only

  # Limit concurrent connections
  tuios-web --max-connections 10

  # All clients share a single session
  tuios-web --default-session shared

  # Use ephemeral mode (no session persistence)
  tuios-web --ephemeral`,
		Version: version,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runWebServer()
		},
		SilenceUsage: true,
	}

	// Web server flags
	rootCmd.Flags().StringVar(&webPort, "port", "7681", "Web server port")
	rootCmd.Flags().StringVar(&webHost, "host", "localhost", "Web server host")
	rootCmd.Flags().BoolVar(&webReadOnly, "read-only", false, "Disable input from clients (view only)")
	rootCmd.Flags().IntVar(&webMaxConnections, "max-connections", 0, "Maximum concurrent connections (0 = unlimited)")
	rootCmd.Flags().StringVar(&webTLSCert, "cert", "", "Path to a TLS certificate in PEM form (serves HTTPS; required to bind a non-loopback host)")
	rootCmd.Flags().StringVar(&webTLSKey, "key", "", "Path to the TLS private key in PEM form (required with --cert)")
	rootCmd.Flags().BoolVar(&webAutoTLS, "auto-tls", false, "Serve HTTPS from a self-signed certificate tuios-web generates and keeps (see `tuios-web cert`)")
	rootCmd.Flags().BoolVar(&webInsecure, "insecure", false, "Serve a non-loopback host over plain HTTP, sending every keystroke unencrypted (trusted networks only)")
	registerCertFlags(rootCmd)
	rootCmd.Flags().StringVar(&webTouch, "touch", "auto", "Whether a client is driven by a finger, which widens the gestures aimed at a single cell: auto, on, off")

	// Daemon mode flags
	rootCmd.Flags().StringVar(&defaultSession, "default-session", "", "Default session name for all connections (creates shared session)")
	rootCmd.Flags().BoolVar(&ephemeralMode, "ephemeral", false, "Disable daemon mode (sessions don't persist)")

	// TUIOS forwarded flags
	rootCmd.Flags().BoolVar(&debugMode, "debug", false, "Enable debug logging")
	rootCmd.Flags().BoolVar(&asciiOnly, "ascii-only", false, "Use ASCII characters instead of Nerd Font icons")
	rootCmd.Flags().StringVar(&themeName, "theme", "", "Color theme to use (e.g., dracula, nord, tokyonight)")
	rootCmd.Flags().StringVar(&borderStyle, "border-style", "", "Window border style: rounded, normal, thick, double, hidden, block, ascii, outer-half-block, inner-half-block")
	rootCmd.Flags().StringVar(&dockbarPosition, "dockbar-position", "", "Dockbar position: bottom, top, hidden")
	rootCmd.Flags().BoolVar(&hideWindowButtons, "hide-window-buttons", false, "Hide window control buttons (minimize, maximize, close)")
	rootCmd.Flags().IntVar(&scrollbackLines, "scrollback-lines", 0, "Number of lines to keep in scrollback buffer (default: 10000, min: 100, max: 1000000)")
	rootCmd.Flags().BoolVar(&showKeys, "show-keys", false, "Enable showkeys overlay to display pressed keys")
	rootCmd.Flags().BoolVar(&noAnimations, "no-animations", false, "Disable UI animations for instant transitions")

	rootCmd.AddCommand(newCertCmd())

	// Execute with fang
	if err := fang.Execute(
		context.Background(),
		rootCmd,
		fang.WithVersion(fmt.Sprintf("%s\nCommit: %s\nBuilt: %s\nBy: %s", version, commit, date, builtBy)),
	); err != nil {
		os.Exit(1)
	}
}

func runWebServer() error {
	// Refuse an unencrypted LAN bind before anything is started, so the user
	// gets the answer instead of a daemon and a half-open port.
	if err := checkTransportSecurity(os.Stderr); err != nil {
		return err
	}

	// Settle the keypair here too, for the same reason: generating it can
	// fail, and a failure should not leave a daemon running behind it.
	tlsCert, tlsKey, err := resolveTLSFiles(os.Stderr)
	if err != nil {
		return err
	}

	// CRITICAL: Force lipgloss to use TrueColor BEFORE any styles are created.
	// By default, lipgloss detects color profile from os.Stdout, which isn't a TTY
	// when running as a web server. This causes all colors to be stripped.
	lipgloss.Writer.Profile = colorprofile.TrueColor

	// Set terminal environment variables
	_ = os.Setenv("TERM", "xterm-256color")
	_ = os.Setenv("COLORTERM", "truecolor")

	if debugMode {
		_ = os.Setenv("TUIOS_DEBUG_INTERNAL", "1")
	}

	// Store server config for handler
	webServerConfig.defaultSession = defaultSession
	webServerConfig.ephemeral = ephemeralMode
	webServerConfig.version = version

	// If using daemon mode, ensure daemon is running
	if !ephemeralMode {
		if err := session.EnsureDaemonRunning(); err != nil {
			log.Printf("Warning: Failed to start daemon, falling back to ephemeral mode: %v", err)
			webServerConfig.ephemeral = true
		}
	}

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.Println("Shutting down...")
		cancel()
		// Stop in-process daemon if we started one
		session.StopInProcessDaemon()

		// Force exit after short timeout or on second signal
		go func() {
			select {
			case <-c:
				os.Exit(0)
			case <-time.After(1 * time.Second):
				os.Exit(0)
			}
		}()
	}()

	// Advertise kitty graphics support to child processes. tuios-web runs
	// atop sip+xterm.js with xterm-addon-image (kittySupport=true), so the
	// host terminal understands kitty graphics. Apps like `kitten icat` and
	// `yazi` refuse to emit graphics unless TERM looks kitty-aware, so we
	// override TERM/TERM_PROGRAM here. This is per-process and affects all
	// web sessions spawned by this tuios-web instance. getTerminalEnv() in
	// internal/terminal caches on first call via sync.Once, so we must set
	// this BEFORE any window is created.
	_ = os.Setenv("TERM", "xterm-kitty")
	_ = os.Setenv("COLORTERM", "truecolor")
	_ = os.Setenv("TERM_PROGRAM", "tuios-web")

	// Who owns which half of the configuration, for this process:
	//
	// The appearance globals in internal/config are the SERVER's. They are read
	// by every served session's render loop, so they are written once, here, on
	// the startup goroutine, from the config file as it stands at startup. A
	// later connection never rewrites them; that would change the picture under
	// sessions that are already drawing. The same rule, for the same reason, is
	// what internal/server/ssh.go's applyAppearanceOnce enforces.
	//
	// The *UserConfig handed to each session is the SESSION's. Every session
	// loads its own copy (see createDaemonTUIOSInstance), so the settings page
	// can mutate it without touching another client's, and it is never written
	// back to the operator's file. See app.OSOptions.ConfigReadOnly.
	//
	// This call used to pass nil, which is what made a served session ignore
	// the file: everything reaching the client through a package global (theme,
	// borders, dock, sidebar, scrollbar, which-key, notification timings) fell
	// back to the built-in default, and the handful of settings that ride on
	// *UserConfig (startup, hooks, agent alerts, keybindings) applied. Half the
	// config arriving looked more like a rendering bug than a missing load.
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		log.Printf("Warning: Failed to load config, using defaults: %v", err)
		userConfig = config.DefaultConfig()
	}

	// The file is the baseline; CLI flags win. Order matters: ApplyOverrides
	// layers the flags on top of what this leaves behind.
	config.ApplyAppearanceConfig(userConfig)

	config.ApplyOverrides(config.Overrides{
		ASCIIOnly:         asciiOnly,
		BorderStyle:       borderStyle,
		DockbarPosition:   dockbarPosition,
		HideWindowButtons: hideWindowButtons,
		ScrollbackLines:   scrollbackLines,
		NoAnimations:      noAnimations,
		ThemeName:         themeName,
	}, userConfig)

	// Create sip server
	sipConfig := sip.DefaultConfig()
	sipConfig.Host = webHost
	sipConfig.Port = webPort
	sipConfig.ReadOnly = webReadOnly
	sipConfig.MaxConnections = webMaxConnections
	sipConfig.Debug = debugMode
	sipConfig.TLSCert = tlsCert
	sipConfig.TLSKey = tlsKey
	sipConfig.AllowInsecureNoTLS = webInsecure

	// The touch key bar is server-wide while the keys it carries are user
	// settings, so it is built from the startup config read above rather than
	// from a second load that could disagree with the globals.
	leader := config.LeaderKey
	if userConfig.Keybindings.LeaderKey != "" {
		leader = userConfig.Keybindings.LeaderKey
	}
	sipConfig.MobilePrefix, sipConfig.MobileRows = mobileBar(config.NewKeybindRegistry(userConfig), leader)

	// Whether the far end is a finger is decided once, at the handshake, and
	// carried into the session on its context. See touch.go for why the
	// handshake is the only place left to ask.
	touch, ok := parseTouchMode(webTouch)
	if !ok {
		return fmt.Errorf("--touch is %q: it takes auto, on or off", webTouch)
	}
	sipConfig.ConnectMiddleware = append(sipConfig.ConnectMiddleware, touchMiddleware(touch))

	server := sip.NewServer(sipConfig)

	// Log startup mode
	mode := "daemon"
	if webServerConfig.ephemeral {
		mode = "ephemeral"
	}
	log.Printf("Starting web server on %s (mode: %s)", serverURL(), mode)
	if webInsecure && !isLoopbackHost(webHost) {
		log.Printf("Insecure: %s is served over plain HTTP, so anyone on this network can read what you type", serverURL())
	}

	// Serve TUIOS using sip
	return server.Serve(ctx, createTUIOSHandler)
}

// isLoopbackHost reports whether a bind address keeps traffic inside this
// machine. It mirrors the check sip makes when it decides whether TLS is
// mandatory, so the two agree on which binds need a certificate.
func isLoopbackHost(host string) bool {
	if host == "" || host == "localhost" {
		return true
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// resolveTLSFiles decides which keypair the server serves from, generating
// sip's managed one when --auto-tls asked for it and there is none, or the one
// on disk has expired or stopped covering the address being bound.
//
// An explicit --cert wins: sip's own resolveAutoTLS defers to a configured
// keypair, and deferring the same way here keeps the two from disagreeing
// about which certificate is in use.
func resolveTLSFiles(w io.Writer) (certFile, keyFile string, err error) {
	if !webAutoTLS || webTLSCert != "" {
		return webTLSCert, webTLSKey, nil
	}
	cert, created, err := sip.EnsureManagedCert(sip.CertOptions{
		Dir:      webCertDir,
		Hosts:    webCertHosts,
		BindHost: webHost,
		Validity: certValidity(),
	})
	if err != nil {
		return "", "", fmt.Errorf("auto TLS: %w", err)
	}
	// Said before the browser says it. An unexplained "Your connection is not
	// private" on a tool that just reported success reads as the tool being
	// broken, and the next move is usually --insecure forever.
	if created {
		fmt.Fprintf(w, "\nGenerated a TLS certificate: %s\n\n%s\n\n", cert.CertFile, sip.SelfSignedWarning)
	}
	return cert.CertFile, cert.KeyFile, nil
}

// serverURL is the address to open, so a startup line can be pasted or
// tapped rather than assembled by the reader. A wildcard bind answers on
// every address this machine has, and localhost is the one that always
// works from here.
func serverURL() string {
	scheme := "http"
	if webTLSCert != "" || webAutoTLS {
		scheme = "https"
	}
	host := webHost
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		host = "localhost"
	}
	if host == "" {
		host = "localhost"
	}
	return fmt.Sprintf("%s://%s", scheme, net.JoinHostPort(host, webPort))
}

// checkTransportSecurity stops a bind that would carry keystrokes in clear
// text over a network, and answers with the commands that fix it.
//
// sip enforces the same rule, but it states the escape hatch as
// AllowInsecureNoTLS, a Go field of its config that nobody holding this
// binary can reach. Deciding here means the message can name the flags this
// command actually has, filled in with the address the user typed.
//
// Nothing here asks a question first. A prompt would make the same command do
// different things depending on whether stdout is a terminal, and tuios-web is
// started by unit files at least as often as by hand. What everyone gets is
// the refusal, and the flag that answers it.
func checkTransportSecurity(w io.Writer) error {
	if (webTLSCert == "") != (webTLSKey == "") {
		// Leading with a flag name would come out capitalized by fang's
		// error rendering.
		return errors.New("pass both --cert and --key, or neither: a certificate is no use without its key")
	}
	if webTLSCert != "" || webAutoTLS || webInsecure || isLoopbackHost(webHost) {
		return nil
	}

	// Printed here rather than carried in the error: fang reflows an error
	// into a paragraph, which would run the commands together and leave
	// nothing to copy.
	fmt.Fprintf(w, `
  %s is not this machine, and without TLS every keystroke you send it, and
  everything a shell prints back, crosses the network in clear text. So pick
  how you want to reach it:

  1. Over HTTPS, from a certificate tuios-web generates and keeps.

       tuios-web --host %s --port %s --auto-tls

     It signs for itself, so every browser warns once per device and you
     accept it there. `+"`tuios-web cert info`"+` says what the warning looks
     like and how to stop seeing it.

  2. Over HTTPS, from a certificate you already have. One from your own CA,
     or a real one, never warns.

       tuios-web --host %s --port %s --cert cert.pem --key key.pem

  3. Left on this machine, reached through SSH. No certificate involved.

       ssh -L %s:localhost:%s <this-machine>

     then open http://localhost:%s at the far end.

  4. In clear text, on a network you trust and on no other.

       tuios-web --host %s --port %s --insecure

`,
		webHost,
		webHost, webPort,
		webHost, webPort,
		webPort, webPort,
		webPort,
		webHost, webPort)

	return fmt.Errorf("refusing to serve %s in clear text: pass --auto-tls, or --cert and --key, or --insecure to accept it", webHost)
}

// createTUIOSHandler creates a TUIOS instance for each web session.
//
// Graphics: starting with sip v0.1.12, the bundled xterm.js loads
// @xterm/addon-image 0.10.0-beta.196 with kittySupport and sixelSupport
// enabled (from xtermjs/xterm.js#5619). We force-enable the kitty/sixel
// passthroughs and route their output through the sip session's PTY slave
// so APC sequences emitted by child processes (chafa -f kitty, kitten
// icat, etc.) flow through the same pipe as bubbletea's text output and
// get rendered by the browser's image addon.
func createTUIOSHandler(sess sip.Session) (tea.Model, []tea.ProgramOption) {
	pty := sess.Pty()
	graphicsOut := sess.PtySlave()
	touch := sessionIsTouch(sess.Context())

	// Determine session name
	sessionName := webServerConfig.defaultSession

	// If ephemeral mode or daemon not available, use old behavior
	if webServerConfig.ephemeral {
		return createEphemeralTUIOSInstance(pty.Width, pty.Height, graphicsOut, touch)
	}

	// Try to connect to daemon
	model, opts, err := createDaemonTUIOSInstance(sessionName, pty.Width, pty.Height, graphicsOut, touch)
	if err != nil {
		log.Printf("Warning: Failed to connect to daemon, using ephemeral mode: %v", err)
		return createEphemeralTUIOSInstance(pty.Width, pty.Height, graphicsOut, touch)
	}

	// Close the daemon client when the web session ends, otherwise the client
	// read loop, its socket, and the daemon-side connState leak per connection.
	if o, ok := model.(*app.OS); ok {
		go func() {
			<-sess.Context().Done()
			o.Cleanup()
		}()
	}

	return model, opts
}

// shortID returns the first 8 characters of an id for logging, or the whole id
// when it is shorter, so a non-UUID id cannot panic the log call.
func shortID(id string) string {
	if len(id) < 8 {
		return id
	}
	return id[:8]
}

// createEphemeralTUIOSInstance creates a standalone TUIOS instance (old behavior)
func createEphemeralTUIOSInstance(width, height int, graphicsOut *os.File, touch bool) (tea.Model, []tea.ProgramOption) {
	// Load user configuration
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		userConfig = config.DefaultConfig()
	}

	// Set up the input handler
	app.SetInputHandler(input.HandleInput)

	// Create keybind registry
	keybindRegistry := config.NewKeybindRegistry(userConfig)

	// Create TUIOS instance with kitty/sixel graphics routed through the
	// sip PTY slave. sip v0.1.12+ bundles xterm.js's image addon with
	// kittySupport enabled, so APC sequences we forward here are rendered
	// by the browser terminal.
	tuiosInstance := app.NewOS(app.OSOptions{
		KeybindRegistry:           keybindRegistry,
		UserConfig:                userConfig,
		ConfigReadOnly:            true,
		ShowKeys:                  showKeys,
		Width:                     width,
		Height:                    height,
		EnableGraphicsPassthrough: true,
		ForceGraphicsEnabled:      true,
		GraphicsOutput:            graphicsOut,
		TouchClient:               touch,
	})

	return tuiosInstance, []tea.ProgramOption{
		tea.WithFPS(config.MaxFPSCap),
	}
}

// createDaemonTUIOSInstance creates a TUIOS instance connected to the daemon
func createDaemonTUIOSInstance(sessionName string, width, height int, graphicsOut *os.File, touch bool) (tea.Model, []tea.ProgramOption, error) {
	// Connect to daemon
	client := session.NewTUIClient()
	v := webServerConfig.version
	if v == "" {
		v = "web-client"
	}

	// Advertise kitty graphics capability to the daemon. sip v0.1.12+
	// bundles xterm.js's image addon with kittySupport enabled, so the
	// browser terminal can render kitty APC sequences forwarded by child
	// processes. Cell dimensions are placeholders; the daemon uses them
	// for pixel-perfect sizing hints (kitty icat queries terminal cell
	// size before transmitting) and tuios-web doesn't have access to the
	// browser's real font metrics.
	webCaps := &session.ClientCapabilities{
		KittyGraphics: true,
		SixelGraphics: true,
		TerminalName:  "tuios-web",
		CellWidth:     10,
		CellHeight:    20,
	}
	if err := client.ConnectWithCapabilities(v, width, height, webCaps); err != nil {
		return nil, nil, fmt.Errorf("failed to connect to daemon: %w", err)
	}

	// Determine which session to attach to. The previous behavior  - picking
	// an arbitrary existing session  - was confusing and non-deterministic.
	// New behavior:
	//   - If --default-session is set, use that (create if missing).
	//   - Otherwise attach to a dedicated session named "web" (create if
	//     missing). Users can then `Ctrl+B S` to switch to any other session
	//     from inside TUIOS using the built-in session switcher.
	if sessionName == "" {
		sessionName = "web"
	}

	// Attach to session (create if doesn't exist)
	state, err := client.AttachSession(sessionName, true, width, height)
	if err != nil {
		_ = client.Close()
		return nil, nil, fmt.Errorf("failed to attach to session: %w", err)
	}

	// Start read loop for daemon messages
	client.StartReadLoop()

	// Load user configuration
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		log.Printf("Warning: Failed to load config for web session, using defaults: %v", err)
		userConfig = config.DefaultConfig()
	}
	keybindRegistry := config.NewKeybindRegistry(userConfig)

	// Set up the input handler
	app.SetInputHandler(input.HandleInput)

	// Create TUIOS instance connected to daemon. Graphics passthrough is
	// force-enabled and routed through the sip PTY slave so kitty/sixel
	// sequences reach the browser's xterm.js image addon (sip v0.1.12+).
	tuiosInstance := app.NewOS(app.OSOptions{
		KeybindRegistry:           keybindRegistry,
		UserConfig:                userConfig,
		ConfigReadOnly:            true,
		ShowKeys:                  showKeys,
		Width:                     width,
		Height:                    height,
		IsDaemonSession:           true,
		DaemonClient:              client,
		SessionName:               sessionName,
		EnableGraphicsPassthrough: true,
		ForceGraphicsEnabled:      true,
		GraphicsOutput:            graphicsOut,
		TouchClient:               touch,
	})

	// Restore state from daemon if available
	if state != nil && len(state.Windows) > 0 {
		log.Printf("[WEB] Restoring %d windows from session state", len(state.Windows))
		if err := tuiosInstance.RestoreFromState(state); err != nil {
			log.Printf("Warning: Failed to restore session state: %v", err)
		}

		// Restore terminal states
		if err := tuiosInstance.RestoreTerminalStates(); err != nil {
			log.Printf("Warning: Failed to restore terminal states: %v", err)
		}

		// Set up PTY output handlers for existing windows (workspace-aware)
		// This only subscribes to PTYs for windows in the current workspace
		if err := tuiosInstance.SetupPTYOutputHandlers(); err != nil {
			log.Printf("Warning: Failed to setup PTY handlers: %v", err)
		}

		// Sync daemon PTY dimensions to match window dimensions from state
		// This fixes the issue where PTYs have stale dimensions after detach/reattach
		tuiosInstance.SyncDaemonPTYDimensions()
	}

	// Register multi-client handlers
	registerMultiClientHandlers(tuiosInstance, client)

	return tuiosInstance, []tea.ProgramOption{
		tea.WithFPS(config.MaxFPSCap),
	}, nil
}

// registerMultiClientHandlers registers handlers for multi-client messages
func registerMultiClientHandlers(m *app.OS, client *session.TUIClient) {
	// Handle state sync from other clients via channel (thread-safe)
	client.OnStateSync(func(state *session.SessionState, triggerType, sourceID string) {
		log.Printf("[WEB] Received state sync: trigger=%s, source=%s", triggerType, shortID(sourceID))
		// Send state to channel for processing in Bubble Tea event loop
		// This ensures thread-safe access to m.Windows
		if m.StateSyncChan != nil {
			select {
			case m.StateSyncChan <- state:
			default:
				log.Printf("[WEB] Warning: StateSyncChan full, dropping state sync")
			}
		}
	})

	// Handle client join notifications via channel (thread-safe)
	client.OnClientJoined(func(clientID string, clientCount int, width, height int) {
		log.Printf("[WEB] Client joined: %s (total: %d, size: %dx%d)", shortID(clientID), clientCount, width, height)
		if m.ClientEventChan != nil {
			select {
			case m.ClientEventChan <- app.ClientEvent{Type: "joined", ClientID: clientID, ClientCount: clientCount, Width: width, Height: height}:
			default:
				log.Printf("[WEB] Warning: ClientEventChan full, dropping client joined event")
			}
		}
	})

	// Handle client leave notifications via channel (thread-safe)
	client.OnClientLeft(func(clientID string, clientCount int) {
		log.Printf("[WEB] Client left: %s (remaining: %d)", shortID(clientID), clientCount)
		if m.ClientEventChan != nil {
			select {
			case m.ClientEventChan <- app.ClientEvent{Type: "left", ClientID: clientID, ClientCount: clientCount}:
			default:
				log.Printf("[WEB] Warning: ClientEventChan full, dropping client left event")
			}
		}
	})

	// Handle session resize (min of all clients). The callback runs on the daemon
	// read-loop goroutine, so the actual geometry mutation (TileAllWindows,
	// emulator resizes) must happen in Update; route it through the event channel.
	client.OnSessionResize(func(width, height, clientCount int) {
		log.Printf("[WEB] Session resize: %dx%d (clients: %d)", width, height, clientCount)
		if m.ClientEventChan != nil {
			select {
			case m.ClientEventChan <- app.ClientEvent{Type: "resize", ClientCount: clientCount, Width: width, Height: height}:
			default:
				log.Printf("[WEB] Warning: ClientEventChan full, dropping session resize event")
			}
		}
	})

	// Handle force refresh (also on the read-loop goroutine; MarkAllDirty must run
	// on the program goroutine).
	client.OnForceRefresh(func(reason string) {
		log.Printf("[WEB] Force refresh requested: %s", reason)
		if m.ClientEventChan != nil {
			select {
			case m.ClientEventChan <- app.ClientEvent{Type: "refresh", Reason: reason}:
			default:
				log.Printf("[WEB] Warning: ClientEventChan full, dropping force refresh event")
			}
		}
	})
}
