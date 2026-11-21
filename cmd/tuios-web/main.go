// Package main implements tuios-web, a web-based terminal server for TUIOS.
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
	"sync"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/sip"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/cliflags"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/debuglog"
	"github.com/Gaurav-Gosain/tuios/internal/fang"
	"github.com/Gaurav-Gosain/tuios/internal/netutil"
	"github.com/Gaurav-Gosain/tuios/internal/release"
	"github.com/Gaurav-Gosain/tuios/internal/served"
	"github.com/Gaurav-Gosain/tuios/internal/serverscope"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/termio"
	"github.com/charmbracelet/colorprofile"
	"github.com/spf13/cobra"
	"tuios.dev/vt"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
	builtBy = "unknown"
)

var webSettings config.Settings

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
	webCtrlVToProgram bool
	webUser           string
	webPasswordFile   string
	webRandomPassword bool
	webNoAuth         bool
	webAllowHosts     []string
	debugMode         bool
	interfaceFlags    cliflags.Interface
	defaultSession    string
	ephemeralMode     bool
)

var webServerConfig struct {
	defaultSession string
	ephemeral      bool
	version        string
}

func newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "tuios-web",
		Short: "Web-based terminal server for TUIOS",
		Long: `tuios-web: Web Terminal Server for TUIOS

Serves TUIOS through the browser with full terminal emulation capabilities.
Powered by sip (github.com/Gaurav-Gosain/sip).

Server features:
  - Dual protocol support: WebTransport (HTTP/3 over QUIC) for low latency
    with automatic WebSocket fallback for broader compatibility
  - HTTPS from a self-signed certificate tuios-web generates and keeps
    (--auto-tls), or from your own (--cert/--key). A bind to a LAN address
    requires one of them unless you opt into clear text with --insecure
  - A password for every browser that connects. A bind to a LAN address
    requires one (--random-password, --password-file or TUIOS_WEB_PASSWORD)
    unless you pass --no-auth. On localhost with no password, other users on
    this machine can connect. Use --password-file for a fixed password:
    tuios-web does not pass TUIOS_WEB_PASSWORD to panes, but your own
    processes can read it from /proc
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

  # Reach the server from a phone on the same network, over TLS, with a
  # password that tuios-web makes and prints
  tuios-web --host 0.0.0.0 --auto-tls --random-password

  # Same, with a certificate and a password you already have
  tuios-web --host 0.0.0.0 --cert cert.pem --key key.pem --password-file ~/.config/tuios/web-password

  # Same, on a network you trust, with nothing encrypted and no password.
  # --no-auth needs the address of one interface. It does not work with 0.0.0.0
  tuios-web --host 192.168.1.20 --insecure --no-auth

  # Behind a reverse proxy on this machine that sends its own host name.
  # --allow-host needs a password or --no-auth
  tuios-web --allow-host term.example.com --password-file ~/.config/tuios/web-password

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

	rootCmd.Flags().StringVar(&webPort, "port", "7681", "Web server port")
	rootCmd.Flags().StringVar(&webHost, "host", "localhost", "Web server host")
	rootCmd.Flags().BoolVar(&webReadOnly, "read-only", false, "Disable input from clients (view only)")
	rootCmd.Flags().IntVar(&webMaxConnections, "max-connections", 0, "Maximum concurrent connections (0 = unlimited)")
	rootCmd.Flags().StringVar(&webTLSCert, "cert", "", "Path to a TLS certificate in PEM form (serves HTTPS, required to bind a non-loopback host)")
	rootCmd.Flags().StringVar(&webTLSKey, "key", "", "Path to the TLS private key in PEM form (required with --cert)")
	rootCmd.Flags().BoolVar(&webAutoTLS, "auto-tls", false, "Serve HTTPS from a self-signed certificate tuios-web generates and keeps (see `tuios-web cert`)")
	rootCmd.Flags().BoolVar(&webInsecure, "insecure", false, "Serve a non-loopback host over plain HTTP, sending every keystroke unencrypted (trusted networks only)")
	rootCmd.Flags().StringVar(&webUser, "user", defaultWebUser, "User name the browser must give with the password")
	rootCmd.Flags().StringVar(&webPasswordFile, "password-file", "", "Read the password from the first line of this file. The file must be yours, with mode 600 or 400 (on Windows, only you, SYSTEM and Administrators may read it)")
	rootCmd.Flags().BoolVar(&webRandomPassword, "random-password", false, "Make a new password at start and print it")
	rootCmd.Flags().BoolVar(&webNoAuth, "no-auth", false, "Serve a non-loopback host with no password. Anyone who reaches the port gets a shell (trusted networks only). Not with 0.0.0.0 or ::")
	rootCmd.Flags().StringSliceVar(&webAllowHosts, "allow-host", nil, "On a loopback --host, also accept this host name (no port) in the Host header, for example for a reverse proxy. Needs a password or --no-auth (repeatable)")
	registerCertFlags(rootCmd)
	rootCmd.Flags().StringVar(&webTouch, "touch", "auto", "Touch input mode: auto, on, off. Touch widens the gestures aimed at a single cell")
	rootCmd.Flags().BoolVar(&webCtrlVToProgram, "ctrl-v-to-program", false, "Send Ctrl+V to the program in the pane. By default Ctrl+V pastes")

	rootCmd.Flags().StringVar(&defaultSession, "default-session", "", "Default session name for all connections (creates shared session)")
	rootCmd.Flags().BoolVar(&ephemeralMode, "ephemeral", false, "Disable daemon mode (sessions don't persist)")

	rootCmd.Flags().BoolVar(&debugMode, "debug", false, "Enable debug logging")
	interfaceFlags.Register(rootCmd.Flags())

	rootCmd.AddCommand(newCertCmd())
	return rootCmd
}

func main() {
	applyBuildIdentity()

	if err := serverscope.ApplyEnv(); err != nil {
		fmt.Fprintf(os.Stderr, "tuios-web: %v.\n", err)
		os.Exit(1)
	}

	rootCmd := newRootCmd()

	if err := fang.Execute(
		context.Background(),
		rootCmd,
		fang.WithVersion(fmt.Sprintf("%s\nCommit: %s\nBuilt: %s\nBy: %s", version, commit, date, builtBy)),
	); err != nil {
		os.Exit(1)
	}
}

func accessFlags() webAccessFlags {
	return webAccessFlags{
		host:           webBindAddr,
		port:           webPort,
		tls:            webTLSCert != "" || webAutoTLS,
		user:           webUser,
		passwordFile:   webPasswordFile,
		randomPassword: webRandomPassword,
		noAuth:         webNoAuth,
		allowHosts:     webAllowHosts,
		tlsArgs:        tlsFlagsForMenu(),
	}
}

func tlsFlagsForMenu() string {
	switch {
	case webTLSCert != "":
		return " --cert " + webTLSCert + " --key " + webTLSKey
	case webAutoTLS:
		return " --auto-tls"
	case webInsecure:
		return " --insecure"
	}
	return ""
}

func runWebServer() error {
	bind, err := resolveBindHost(context.Background(), netResolver, webHost)
	if err != nil {
		return err
	}
	webBindAddr = bind

	access, err := planWebAccess(os.Stderr, accessFlags())
	if err != nil {
		return err
	}
	if err := checkTransportSecurity(os.Stderr); err != nil {
		return err
	}
	access.announcePassword(os.Stderr)

	tlsCert, tlsKey, err := resolveTLSFiles(os.Stderr)
	if err != nil {
		return err
	}

	app.SetAccentColorProfile(colorprofile.TrueColor)

	app.SetClientCapabilities(webHostCaps(webFallbackCellWidth, webFallbackCellHeight))

	pinGuestTerminalEnv()

	if debugMode {
		debuglog.Enable()
	}

	webServerConfig.defaultSession = defaultSession
	webServerConfig.ephemeral = ephemeralMode
	webServerConfig.version = version

	userConfig, err := config.LoadUserConfig()
	if err != nil {
		log.Printf("Warning: Failed to load config, using defaults: %v", err)
		userConfig = config.DefaultConfig()
	}

	if !ephemeralMode {
		if err := session.EnsureDaemonRunningWith(version, daemonConfigFrom(userConfig)); err != nil {
			log.Printf("Warning: Failed to start daemon, falling back to ephemeral mode: %v", err)
			webServerConfig.ephemeral = true
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.Println("Shutting down...")
		cancel()
		session.StopInProcessDaemon()

		go func() {
			select {
			case <-c:
				os.Exit(0)
			case <-time.After(1 * time.Second):
				os.Exit(0)
			}
		}()
	}()

	webSettings = config.Resolve(userConfig, webAppearanceOverrides())

	sipConfig := sip.DefaultConfig()
	sipConfig.Host = webBindAddr
	sipConfig.Port = webPort
	sipConfig.ReadOnly = webReadOnly
	sipConfig.MaxConnections = webMaxConnections
	sipConfig.Debug = debugMode
	sipConfig.TLSCert = tlsCert
	sipConfig.TLSKey = tlsKey
	sipConfig.AllowInsecureNoTLS = webInsecure
	sipConfig.MaxWindowDims = sip.WindowSize{Width: webMaxCols, Height: webMaxRows}
	sipConfig.MaxWindowCells = webMaxCells
	access.apply(&sipConfig)

	sipConfig.Appearance = browserAppearance()

	webPalette = newBrowserPalette(sipConfig.Appearance.Theme)
	app.SetClientCapabilities(webHostCaps(webFallbackCellWidth, webFallbackCellHeight))

	leader := webSettings.LeaderKey
	if userConfig.Keybindings.LeaderKey != "" {
		leader = userConfig.Keybindings.LeaderKey
	}
	sipConfig.MobilePrefix, sipConfig.MobileRows = mobileBar(config.NewKeybindRegistry(userConfig), leader)

	touch, ok := parseTouchMode(webTouch)
	if !ok {
		return fmt.Errorf("--touch is %q: it takes auto, on or off", webTouch)
	}
	sipConfig.ConnectMiddleware = append(sipConfig.ConnectMiddleware, touchMiddleware(touch), appleMiddleware())
	sipConfig.CtrlVToProgram = webCtrlVToProgram

	installInboxLink(&sipConfig)

	installStripTouch(&sipConfig)

	server := sip.NewServer(sipConfig)

	mode := "daemon"
	if webServerConfig.ephemeral {
		mode = "ephemeral"
	}
	log.Printf("Starting web server on %s (mode: %s)", serverURL(), mode)
	if webInsecure && !isLoopbackHost(webBindAddr) {
		log.Printf("Insecure: %s is served over plain HTTP, so anyone on this network can read what you type", serverURL())
	}

	return server.ServeWithProgram(ctx, createTUIOSProgram)
}

func pinGuestTerminalEnv() {
	_ = os.Setenv("TERM", "xterm-256color")
	_ = os.Setenv("COLORTERM", "truecolor")
}

func createTUIOSProgram(sess sip.Session) *tea.Program {
	model := createTUIOSHandler(sess)
	running := make(chan struct{})
	started := &programStart{Model: model, start: sync.OnceFunc(func() { close(running) })}
	current := app.WithCurrentSize(func() (int, int) {
		pty := sess.Pty()
		return pty.Width, pty.Height
	})
	slave, owned := sessionTerminal(sess)
	ownScreen := slave != nil && model.Settings.CellFramesFor(true)
	opts := append(sip.MakeOptions(sess), app.ProgramOptions(current, app.WithOwnScreen(ownScreen))...)
	if slave != nil {
		frames := app.NewFrameHookFile(slave)
		model.ConnectFrameWriter(frames)
		opts = append(opts, tea.WithOutput(frames))
		if ownScreen {
			model.UseScreen(frames, []string{"TERM=xterm-256color", "COLORTERM=truecolor"}, colorprofile.TrueColor,
				termio.Output{Raw: true, Console: slave})
		}
	}
	program := tea.NewProgram(started, opts...)
	model.BindProgram(program)
	go cleanupAfterProgram(program, running, func() {
		model.CloseScreen()
		if owned {
			_ = slave.Close()
		}
		model.Cleanup()
	})
	return program
}

func sessionTerminal(sess sip.Session) (*os.File, bool) {
	slave := sess.PtySlave()
	if slave == nil {
		return nil, false
	}
	own, err := ownTerminal(slave)
	if err != nil {
		log.Printf("Warning: web session uses the shared terminal file: %v", err)
		return slave, false
	}
	return own, true
}

func cleanupAfterProgram(program *tea.Program, running <-chan struct{}, cleanup func()) {
	<-running
	program.Wait()
	cleanup()
}

type programStart struct {
	tea.Model
	start func()
}

func (p *programStart) Init() tea.Cmd {
	p.start()
	return p.Model.Init()
}

func daemonConfigFrom(userConfig *config.UserConfig) *session.DaemonConfig {
	return session.DaemonConfigFromUser(userConfig)
}

func isLoopbackHost(host string) bool { return netutil.IsLoopbackHost(host) }

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
	if created {
		fmt.Fprintf(w, "\nGenerated a TLS certificate: %s\n\n%s\n\n", cert.CertFile, sip.SelfSignedWarning)
	}
	return cert.CertFile, cert.KeyFile, nil
}

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

func checkTransportSecurity(w io.Writer) error {
	if (webTLSCert == "") != (webTLSKey == "") {
		return errors.New("pass both --cert and --key, or neither: a certificate is no use without its key")
	}
	if webTLSCert != "" || webAutoTLS || webInsecure || isLoopbackHost(webBindAddr) {
		return nil
	}

	auth := authFlagsForMenu(accessFlags())
	fmt.Fprintf(w, `
  %s is not this machine, and without TLS every keystroke you send it, and
  everything a shell prints back, crosses the network in clear text. So pick
  how you want to reach it:

  1. Over HTTPS, from a certificate tuios-web generates and keeps.

       tuios-web --host %s --port %s --auto-tls%s

     The certificate is self-signed, so every browser warns once per device.
     Accept the warning there. `+"`tuios-web cert info`"+` says what the warning
     looks like and how to stop seeing it.

  2. Over HTTPS, from a certificate you already have. One from your own CA,
     or a real one, never warns.

       tuios-web --host %s --port %s --cert cert.pem --key key.pem%s

  3. Left on this machine, reached through SSH. No certificate involved.

       ssh -L %s:localhost:%s <this-machine>

     then open http://localhost:%s at the far end.

  4. In clear text. Only on a network you trust.

       tuios-web --host %s --port %s --insecure%s

  TLS encrypts the connection. The password decides who can connect.

`,
		webHost,
		webHost, webPort, auth,
		webHost, webPort, auth,
		webPort, webPort,
		webPort,
		webHost, webPort, auth)

	return fmt.Errorf("refusing to serve %s in clear text: pass --auto-tls, or --cert and --key, or --insecure to accept it", webHost)
}

const (
	webMaxCols  = 1200
	webMaxRows  = 500
	webMaxCells = 250_000
)

func createTUIOSHandler(sess sip.Session) *app.OS {
	pty := sess.Pty()
	graphicsOut := sess.PtySlave()
	touch := sessionIsTouch(sess.Context())

	hostCaps := webHostCaps(cellSize(pty))

	opts := app.OSOptions{
		Client:         app.ClientBrowser,
		ShowKeys:       interfaceFlags.ShowKeys,
		Width:          pty.Width,
		Height:         pty.Height,
		GraphicsOutput: graphicsOut,
		TouchClient:    touch,
		BrowserPaste: app.BrowserPasteKeys{
			Apple:          sessionIsApple(sess.Context()),
			CtrlVToProgram: webCtrlVToProgram,
		},
		Caps: hostCaps,
	}

	if webServerConfig.ephemeral {
		return served.NewModel(opts, webAppearanceOverrides())
	}

	version := webServerConfig.version
	if version == "" {
		version = "web-client"
	}

	daemonOpts := opts
	daemonOpts.SessionName = webServerConfig.defaultSession
	daemonOpts.ViewOnly = webReadOnly
	daemonOpts.OpenInboxItem = sessionInboxItem(sess.Context())
	model, err := served.Attach(daemonOpts, webAppearanceOverrides(), version, app.ClientCapabilitiesOf(hostCaps, webSettings.ImageSymbols), pickWebSession)
	if err != nil {
		log.Printf("Warning: Failed to connect to daemon, using ephemeral mode: %v", err)
		return served.NewModel(opts, webAppearanceOverrides())
	}
	return model
}

func pickWebSession([]string) string { return "web" }

func webHostCaps(cellWidth, cellHeight int) *app.HostCapabilities {
	caps := &app.HostCapabilities{
		KittyGraphics: true,
		SixelGraphics: false,
		TrueColor:     true,
		TerminalName:  "tuios-web",
		CellWidth:     cellWidth,
		CellHeight:    cellHeight,
	}
	webPalette.applyTo(caps)
	return caps
}

const (
	webFallbackCellWidth  = vt.FallbackCellWidth
	webFallbackCellHeight = vt.FallbackCellHeight
)

func cellSize(pty sip.Pty) (cellWidth, cellHeight int) {
	cellWidth, cellHeight = webFallbackCellWidth, webFallbackCellHeight
	if pty.Width > 0 && pty.WidthPx > 0 {
		if w := pty.WidthPx / pty.Width; w > 0 {
			cellWidth = w
		}
	}
	if pty.Height > 0 && pty.HeightPx > 0 {
		if h := pty.HeightPx / pty.Height; h > 0 {
			cellHeight = h
		}
	}
	return cellWidth, cellHeight
}

func webAppearanceOverrides() config.Overrides {
	return interfaceFlags.Overrides()
}

func applyBuildIdentity() {
	s := app.SetBuildIdentity(release.Stamps{Version: version, Commit: commit, Date: date, BuiltBy: builtBy})
	version, commit, date, builtBy = s.Version, s.Commit, s.Date, s.BuiltBy
}
