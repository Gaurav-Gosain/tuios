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
	"github.com/Gaurav-Gosain/tuios/internal/netutil"
	"github.com/Gaurav-Gosain/tuios/internal/served"
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
	debugMode bool
	// interfaceFlags is the same interface flag set `tuios` registers.
	interfaceFlags cliflags.Interface
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

// newRootCmd builds the tuios-web command with every flag it takes.
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
	rootCmd.Flags().StringVar(&webTLSCert, "cert", "", "Path to a TLS certificate in PEM form (serves HTTPS, required to bind a non-loopback host)")
	rootCmd.Flags().StringVar(&webTLSKey, "key", "", "Path to the TLS private key in PEM form (required with --cert)")
	rootCmd.Flags().BoolVar(&webAutoTLS, "auto-tls", false, "Serve HTTPS from a self-signed certificate tuios-web generates and keeps (see `tuios-web cert`)")
	rootCmd.Flags().BoolVar(&webInsecure, "insecure", false, "Serve a non-loopback host over plain HTTP, sending every keystroke unencrypted (trusted networks only)")
	registerCertFlags(rootCmd)
	rootCmd.Flags().StringVar(&webTouch, "touch", "auto", "Touch input mode: auto, on, off. Touch widens the gestures aimed at a single cell")

	// Daemon mode flags
	rootCmd.Flags().StringVar(&defaultSession, "default-session", "", "Default session name for all connections (creates shared session)")
	rootCmd.Flags().BoolVar(&ephemeralMode, "ephemeral", false, "Disable daemon mode (sessions don't persist)")

	// TUIOS forwarded flags
	rootCmd.Flags().BoolVar(&debugMode, "debug", false, "Enable debug logging")
	interfaceFlags.Register(rootCmd.Flags())

	rootCmd.AddCommand(newCertCmd())
	return rootCmd
}

func main() {
	// See cmd/tuios/main.go: a crash report names the build it came from, and
	// internal/app cannot read these vars itself.
	app.SetBuildStamp(version, commit)

	rootCmd := newRootCmd()

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

	// The accent picker labels colours through its own probe of this process's
	// stdout, which is not a TTY here; pin it to what the browser terminal
	// renders. Frames need no such pin: composeFrame hands the canvas to the
	// bubbletea renderer unchanged, and sip sets that renderer's profile.
	app.SetAccentColorProfile(colorprofile.TrueColor)

	// Install the browser terminal as the process host capabilities. Without
	// this, GetHostCapabilities probes this process's non-TTY stdin and reports
	// no graphics and a 9x20 default cell, disagreeing with the capabilities
	// every connection is given: the same terminal, described two ways.
	//
	// The cell size here is a process-wide default, and stays a placeholder: it
	// is installed once at startup, before any browser has connected, and one
	// process serves several browsers at once at whatever font size each reader
	// chose. Each connection carries its own, measured from that browser's
	// canvas. See cellSize and webHostCaps.
	app.SetClientCapabilities(webHostCaps(webFallbackCellWidth, webFallbackCellHeight))

	pinGuestTerminalEnv()

	if debugMode {
		_ = os.Setenv("TUIOS_DEBUG_INTERNAL", "1")
	}

	// Store server config for handler
	webServerConfig.defaultSession = defaultSession
	webServerConfig.ephemeral = ephemeralMode
	webServerConfig.version = version

	// Who owns which part of the configuration, for this process:
	//
	//   - [daemon] belongs to the DAEMON, which outlives every client, so it is
	//     only honoured by whoever starts it (just below). An already-running
	//     daemon keeps what it started with.
	//   - The appearance globals in internal/config are the SERVER's, written
	//     once at startup because every served session's render loop reads them.
	//   - The *UserConfig handed to a session is the SESSION's, mutable by its
	//     settings page and never written back to the operator's file.
	//
	// One read feeds all three, so they cannot disagree about what the file said.
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		log.Printf("Warning: Failed to load config, using defaults: %v", err)
		userConfig = config.DefaultConfig()
	}

	// If using daemon mode, ensure daemon is running
	if !ephemeralMode {
		// The [daemon] settings reach it the same way `tuios daemon` passes
		// them (see runDaemon). Without this a daemon autostarted here ran on
		// the built-in defaults, so whether agent detection was on depended on
		// which command happened to start the daemon first.
		if err := session.EnsureDaemonRunningWith(version, daemonConfigFrom(userConfig)); err != nil {
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

	// The appearance globals, written once on the startup goroutine. A later
	// connection must never rewrite them: they are read by the render loop of
	// every session already drawing. internal/server/ssh.go's applyAppearanceOnce
	// enforces the same rule for the same reason.
	//
	// ApplyOverrides must get the loaded config, not nil. With nil, everything
	// reaching the client through a package global (theme, borders, dock,
	// sidebar, scrollbar, which-key, notification timings) comes out at its
	// built-in default, while the settings that ride on *UserConfig (startup,
	// hooks, agent alerts, keybindings) still apply, and the half-applied
	// config looks like a rendering bug.
	//
	// The file is the baseline; CLI flags win. Order matters: ApplyOverrides
	// layers the flags on top of what this leaves behind.
	config.ApplyAppearanceConfig(userConfig, &config.Global)

	config.ApplyOverrides(webAppearanceOverrides(), &config.Global)

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

	// How the page looks. Read after the config file and the flags have both
	// landed on the globals above, because that is when theme.Current() is the
	// theme the user asked for. Without it a browser gets sip's own palette
	// whatever the user picked.
	sipConfig.Appearance = browserAppearance()

	// The colours the browser will resolve palette indices to follow from that
	// appearance, so they are settled here too, before any browser connects.
	// The seed installed above is replaced so it carries them as well.
	webPalette = newBrowserPalette(sipConfig.Appearance.Theme)
	app.SetClientCapabilities(webHostCaps(webFallbackCellWidth, webFallbackCellHeight))

	// The touch key bar is server-wide while the keys it carries are user
	// settings, so it is built from the startup config read above rather than
	// from a second load that could disagree with the globals.
	leader := config.Global.LeaderKey
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

	// Serve TUIOS using sip. The program is built here rather than by sip so
	// the shared options go on last: sip's MakeOptions carries a WithFilter of
	// its own, and Serve would append it after ours (see app.ProgramOptions).
	return server.ServeWithProgram(ctx, createTUIOSProgram)
}

// pinGuestTerminalEnv sets the TERM and COLORTERM this process's panes get.
//
// This process's stdout is not the terminal anyone sees, so detecting from it
// would hand an ephemeral pane TERM=dumb. getTerminalEnv in internal/terminal
// trusts the environment when COLORTERM=truecolor is set, and it caches on the
// first window, so this has to run before any window exists.
//
// TERM stays xterm-256color, the value daemon panes get, because a claim such
// as xterm-kitty needs a terminfo entry the server may not have. Image tools
// find kitty graphics through TERM_PROGRAM, which every spawn path sets from
// the passthrough state (see guestenv.TermProgram), so it is not set here.
func pinGuestTerminalEnv() {
	_ = os.Setenv("TERM", "xterm-256color")
	_ = os.Setenv("COLORTERM", "truecolor")
}

// createTUIOSProgram builds the program for one web session.
func createTUIOSProgram(sess sip.Session) *tea.Program {
	model := createTUIOSHandler(sess)
	// running closes when the program's loop reaches the model. See
	// programStart for why the teardown goroutine below has to wait for it.
	running := make(chan struct{})
	started := &programStart{Model: model, start: sync.OnceFunc(func() { close(running) })}
	program := tea.NewProgram(started, append(sip.MakeOptions(sess), app.ProgramOptions()...)...)
	// Tear down after the program has fully stopped, the way the SSH server
	// does. Closing on the session context instead ran Cleanup while the last
	// frames were still going out.
	go cleanupAfterProgram(program, running, model.Cleanup)
	return program
}

// cleanupAfterProgram runs cleanup once the program has stopped for good.
//
// It waits for running before it waits on the program, which is what makes the
// second wait safe from another goroutine. See programStart.
func cleanupAfterProgram(program *tea.Program, running <-chan struct{}, cleanup func()) {
	<-running
	program.Wait()
	cleanup()
}

// programStart is the model a web session's program runs. It reports when the
// program's own loop has reached the model, and delegates everything else.
//
// The report is what makes the teardown goroutine above safe. tea.Program.Wait
// reads a channel that Run creates, and no lock or channel orders that write
// against a Wait on another goroutine (bubbletea v2.0.8 writes it at tea.go:1000
// and reads it at tea.go:1210). sip builds the program through this factory and
// starts the goroutine that calls Run only afterwards, so a Wait started here
// can read the field before Run writes it. That is a data race, and a read that
// lands on the nil zero value blocks for ever, which drops Cleanup in silence.
//
// Run calls the model's Init on its own goroutine, after it creates that
// channel. Closing running from Init therefore puts the write before every
// receive on running, and the receive before Wait. The order is the Go memory
// model's, not a guess about timing.
//
// Init is the only method here. Update and View come from the model, and the
// model returns itself from Update, so this wrapper is gone after the first
// message. The shared motion filter sees it for that one message and passes the
// message on, which is what it does for any model it does not recognise.
//
// One case stays open: Run can fail before it calls Init, while it prepares the
// terminal or the input reader. Then running never closes and Cleanup does not
// run. Closing that needs Run's channel to exist before Run, which only
// bubbletea can do. See the note on Wait in the upstream report.
type programStart struct {
	tea.Model
	start func()
}

// Init reports that the program's loop is running, then inits the model.
func (p *programStart) Init() tea.Cmd {
	p.start()
	return p.Model.Init()
}

// daemonConfigFrom maps the user's [daemon] section onto the daemon's own
// config, mirroring what runDaemon does in cmd/tuios so a daemon autostarted by
// the web server behaves like one started by `tuios daemon`.
func daemonConfigFrom(userConfig *config.UserConfig) *session.DaemonConfig {
	return session.DaemonConfigFromUser(userConfig)
}

// isLoopbackHost reports whether a bind address keeps traffic inside this
// machine. It mirrors the check sip makes when it decides whether TLS is
// mandatory, so the two agree on which binds need a certificate.
//
// The body moved to internal/netutil when `tuios ssh` grew the same kind of
// gate for authentication. One answer to "is this address on the network"
// serves both refusals.
func isLoopbackHost(host string) bool { return netutil.IsLoopbackHost(host) }

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

     The certificate is self-signed, so every browser warns once per device.
     Accept the warning there. `+"`tuios-web cert info`"+` says what the warning
     looks like and how to stop seeing it.

  2. Over HTTPS, from a certificate you already have. One from your own CA,
     or a real one, never warns.

       tuios-web --host %s --port %s --cert cert.pem --key key.pem

  3. Left on this machine, reached through SSH. No certificate involved.

       ssh -L %s:localhost:%s <this-machine>

     then open http://localhost:%s at the far end.

  4. In clear text. Only on a network you trust.

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
func createTUIOSHandler(sess sip.Session) *app.OS {
	pty := sess.Pty()
	graphicsOut := sess.PtySlave()
	touch := sessionIsTouch(sess.Context())

	// This browser's own terminal, measured from its canvas, for either kind
	// of session. Without it the ephemeral path falls back to the process-wide
	// placeholder, and its image cell math uses a 10x20 cell whatever font
	// size the reader has.
	hostCaps := webHostCaps(cellSize(pty))

	// The kind says the rest: read-only config, no desktop, graphics forced on
	// because stdin is not a TTY here. Kitty and sixel output is routed
	// through the sip PTY slave, the same pipe as the text, so the browser's
	// image addon renders it.
	opts := app.OSOptions{
		Client:         app.ClientBrowser,
		ShowKeys:       interfaceFlags.ShowKeys,
		Width:          pty.Width,
		Height:         pty.Height,
		GraphicsOutput: graphicsOut,
		TouchClient:    touch,
		Caps:           hostCaps,
	}

	// If ephemeral mode or daemon not available, use old behavior
	if webServerConfig.ephemeral {
		return served.NewModel(opts, webAppearanceOverrides())
	}

	version := webServerConfig.version
	if version == "" {
		version = "web-client"
	}

	// Try to connect to daemon. The daemon is told this browser's own cell
	// size, measured from the canvas it reports beside its grid (see
	// cellSize), and hands it to every guest as the pixel size of its window.
	// A hardcoded 10x20 there made a tool that asks how big a cell is before
	// drawing, kitty icat being the usual one, draw at the wrong scale.
	daemonOpts := opts
	daemonOpts.SessionName = webServerConfig.defaultSession
	model, err := served.Attach(daemonOpts, webAppearanceOverrides(), version, app.ClientCapabilitiesOf(hostCaps), pickWebSession)
	if err != nil {
		log.Printf("Warning: Failed to connect to daemon, using ephemeral mode: %v", err)
		return served.NewModel(opts, webAppearanceOverrides())
	}
	return model
}

// pickWebSession is the session a browser gets when --default-session names
// none: a dedicated one called "web", created when missing. Picking one of the
// existing sessions instead was arbitrary and changed as sessions came and
// went. The session switcher reaches the others from inside tuios.
func pickWebSession([]string) string { return "web" }

// webHostCaps is the browser terminal one connection draws to. sip's bundled
// xterm.js loads the image addon with kitty and sixel support, so both
// protocols render. KittyAnimation stays false because the browser overlay has
// no a=f frame-edit path, and KittyFileTransfer stays false because the browser
// cannot read server-local paths. The palette is the one the browser draws
// with; see browserPalette.
func webHostCaps(cellWidth, cellHeight int) *app.HostCapabilities {
	caps := &app.HostCapabilities{
		KittyGraphics: true,
		SixelGraphics: true,
		TrueColor:     true,
		TerminalName:  "tuios-web",
		CellWidth:     cellWidth,
		CellHeight:    cellHeight,
	}
	webPalette.applyTo(caps)
	return caps
}

// webFallbackCellWidth and webFallbackCellHeight are what a cell is taken to
// measure when the browser reports no pixel dimensions at all, which is what an
// older client does. They are a guess at a typical monospace cell and nothing
// more; every connection that reports its canvas gets its real measurement.
const (
	webFallbackCellWidth  = 10
	webFallbackCellHeight = 20
)

// cellSize works out what one cell measures in the browser's pixels, from the
// canvas size the browser reports beside its grid. It is a real measurement:
// the browser sends widthPx and heightPx with every resize, so the answer
// follows the reader's font size instead of a number picked at build time.
//
// It falls back to the placeholder when the browser reports no pixels at all,
// which is what an older client does. A cell is never reported as zero: a
// caller multiplying by it would tell a guest its window is zero pixels wide.
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

// webAppearanceOverrides is the interface flags this server layers over the
// config file. It is built per call so a session that connects after an edit
// gets the file as it is now with these flags on top of it, rather than the
// file the server loaded when it started. See config.AppearanceFrom.
func webAppearanceOverrides() config.Overrides {
	return interfaceFlags.Overrides()
}
