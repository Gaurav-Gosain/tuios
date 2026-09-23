// Package server provides SSH server functionality for TUIOS.
package server

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/ssh"
	"charm.land/wish/v2"
	"charm.land/wish/v2/bubbletea"
	"charm.land/wish/v2/logging"
	"github.com/charmbracelet/colorprofile"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/served"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// SSHServerConfig holds configuration for the SSH server.
type SSHServerConfig struct {
	Host           string
	Port           string
	KeyPath        string
	DefaultSession string // If set, all connections attach to this session
	Ephemeral      bool   // If true, don't use daemon (old behavior)
	Version        string // For daemon handshake
	// AuthorizedKeysPath names the file of public keys allowed to connect.
	// Empty searches ~/.config/tuios/authorized_keys, then
	// ~/.ssh/authorized_keys. See auth.go.
	AuthorizedKeysPath string
	// ShowKeys turns the key display overlay on in every served session. It is
	// the --show-keys flag `tuios ssh` registers with the rest of the interface
	// flags.
	ShowKeys bool
	// NoAuth accepts every connection without checking who it is. It is the
	// opt-out that lets a non-loopback bind run with no authorized keys, and
	// the way back in for an operator whose key file locked them out.
	NoAuth bool
	// Overrides carries the interface CLI flags, layered over the appearance
	// baseline inside the same once-guarded application so flags win over the
	// file. The zero value applies nothing.
	Overrides config.Overrides
}

// sshServerContext holds the server-wide context for daemon mode
var sshServerConfig *SSHServerConfig

// applyAppearanceOnce guards the process-wide appearance-config application.
// Once per process, not per server start: the appearance globals are read by
// every session's render loop, so a second StartSSHServer in the same process
// (the test binary does this; a deployment does not) must not rewrite them
// while sessions from an earlier server are still draining.
var applyAppearanceOnce sync.Once

// StartSSHServer initializes and runs the SSH server
func StartSSHServer(ctx context.Context, cfg *SSHServerConfig) error {
	// Who may connect, decided before anything listens. A bind that cannot be
	// served safely is refused here rather than started and warned about: this
	// is the same order cmd/tuios-web uses for TLS, and it is the only order
	// that cannot leave an open port behind while the operator reads the
	// warning. It runs before anything in this process is written, so a
	// refused bind leaves no trace of itself behind.
	//
	// The check lives here and not only in cmd/tuios because this function is
	// the entry point every caller uses, including the tests. A gate that only
	// the command line enforces is a gate the next caller forgets.
	authPlan, err := PlanSSHAuth(cfg.Host, cfg.AuthorizedKeysPath, cfg.NoAuth)
	if err != nil {
		return err
	}

	sshServerConfig = cfg

	// One read of the config file feeds both the appearance globals and the
	// daemon settings below, so the two cannot disagree about what it said.
	// Nil after an error, which each consumer takes as the defaults.
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		log.Printf("Warning: Failed to load config for the SSH server, using defaults: %v", err)
		userConfig = nil
	}

	// Apply the process-wide render globals once, at first server startup and
	// single-threaded, so every per-connection session shares a consistent view
	// of them. LoadUserConfig is pure and NewOS no longer re-applies per
	// connection, so this replaces the old per-connection global writes that
	// raced other sessions' render loops.
	//
	// Frames need no global here: composeFrame hands the canvas to the
	// bubbletea renderer unchanged, and wish configures that renderer per
	// connection from the client's own TERM.
	applyAppearanceOnce.Do(func() {
		// An ephemeral pane's TERM is detected from this process's stdout,
		// and a headless server would hand every one TERM=dumb. This gives
		// them what daemon panes get. A server started in a real terminal
		// still detects from it.
		terminal.SetHeadlessGuestTerm("xterm-256color", "truecolor")

		// The process-wide capability seed. Every SSH session carries its own
		// client's capabilities (OSOptions.Caps), so this is only what a reader
		// with no session in reach sees. It is neutral, no graphics, and it
		// exists so such a reader never probes this server's own stdin.
		app.SetClientCapabilities(&app.HostCapabilities{TrueColor: true})

		if userConfig != nil {
			config.ApplyAppearanceConfig(userConfig, &config.Global)
		}
		// Flags over file, the same order loadAndApplyConfig gives every
		// other entrypoint.
		config.ApplyOverrides(cfg.Overrides, &config.Global)
	})

	// Determine host key path
	var hostKeyPath string
	if cfg.KeyPath != "" {
		hostKeyPath = cfg.KeyPath
	} else {
		// Use default path in .ssh directory
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get user home directory: %w", err)
		}
		hostKeyPath = filepath.Join(homeDir, ".ssh", "tuios_host_key")
	}

	// If using daemon mode, ensure daemon is running.
	//
	// The hook tables go with it. This daemon runs in this process, and a served
	// client leaves the session-side events to it, so a daemon started here
	// without them would stop running those commands rather than run them twice.
	if !cfg.Ephemeral {
		// The [daemon] section, the hosts and the hooks, mapped the same way
		// `tuios daemon` maps them, so a daemon this server starts gets the
		// agent detection settings and the hosts, not only the hooks.
		daemonCfg := session.DaemonConfigFromUser(userConfig)
		if err := session.EnsureDaemonRunningWith(cfg.Version, daemonCfg); err != nil {
			log.Printf("Warning: Failed to start daemon, falling back to ephemeral mode: %v", err)
			cfg.Ephemeral = true
		}
	}

	// Create SSH server with middleware
	opts := []ssh.Option{
		wish.WithAddress(net.JoinHostPort(cfg.Host, cfg.Port)),
		wish.WithHostKeyPath(hostKeyPath),
		wish.WithMiddleware(
			// Bubble Tea middleware for interactive sessions
			tuiosSessionMiddleware(),
			// Logging middleware for connection tracking
			logging.Middleware(),
			// Outermost backstop: contain any panic in a single session's
			// handler chain so it can never take down the whole SSH server (and
			// with it every other connected user). wish runs the last-listed
			// middleware outermost, so this wraps everything above.
			recoverMiddleware(),
		),
	}

	// The authentication handler, or the warning that says there is none.
	//
	// Installing no handler is not an oversight when the plan says so: charm's
	// ssh sets NoClientAuth when every handler is nil, which is what lets a
	// client with no key at all attach on loopback. Installing a handler that
	// returns true would break that, because such a client offers no key to
	// hand it.
	if authPlan.Authenticated() {
		opts = append(opts, wish.WithPublicKeyAuth(publicKeyHandler(authPlan.Keys.Path)))
		log.Printf("SSH authentication is on. %d key(s) from %s", len(authPlan.Keys.Keys), authPlan.Keys.Path)
	} else {
		// Once, at startup. Not per connection: a line on every connect is a
		// line nobody reads, and this one has to be read.
		log.Print(authPlan.Warning)
	}

	server, err := wish.NewServer(opts...)
	if err != nil {
		return fmt.Errorf("failed to create SSH server: %w", err)
	}

	// Start server
	go func() {
		mode := "daemon"
		if cfg.Ephemeral {
			mode = "ephemeral"
		}
		log.Printf("Starting SSH server on %s (mode: %s)", server.Addr, mode)
		if err := server.ListenAndServe(); err != nil {
			log.Printf("SSH server error: %v", err)
		}
	}()

	// Wait for context cancellation
	<-ctx.Done()

	// Shutdown server gracefully. The caller's context is already canceled at
	// this point, so passing it would make Shutdown return immediately without
	// waiting for the per-session handlers to finish; use a fresh bounded
	// context so live sessions get to wind down before the process (or the
	// next test's server) moves on.
	log.Println("Shutting down SSH server...")
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	return server.Shutdown(shutdownCtx)
}

// recoverMiddleware wraps a session handler so a panic in it (or any inner
// middleware) is recovered, logged, and confined to that one session. Bubble
// Tea already recovers panics inside its own program loop and returns from
// Run; this is the backstop for everything outside that loop (session setup,
// capability detection, the daemon connect/restore path), so a single bad
// session can never crash the long-lived server process.
func recoverMiddleware() wish.Middleware {
	return func(next ssh.Handler) ssh.Handler {
		return func(sess ssh.Session) {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("recovered panic in SSH session handler: %v\n%s", r, debug.Stack())
				}
			}()
			next(sess)
		}
	}
}

// serialWriter serializes Write calls to an underlying writer. Both the
// bubbletea renderer (text frames) and the kitty/sixel graphics passthrough
// write to the same SSH session from different goroutines. x/crypto's
// channel.WriteExtended is NOT safe for concurrent use: concurrent writers
// share one packet buffer (packetPool), so overlapping writes corrupt the
// channel-data header inside an otherwise valid transport packet. The client
// then fails the stream with "ssh: wrong packet length" and drops the whole
// connection. A kitty graphics flood from several writers kills the session
// that way. Every writer to the session must go through one shared
// serialWriter.
type serialWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *serialWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// tuiosSessionMiddleware runs the TUIOS bubbletea program for each SSH
// session. It replaces wish's stock bubbletea.Middleware for two reasons:
//
//  1. The program's text output and the graphics passthrough output must be
//     the SAME serialized writer around the session (see serialWriter). The
//     stock middleware appends MakeOptions last, so its WithOutput(session)
//     would override ours; here MakeOptions is applied first and the
//     serialized writer wins.
//  2. Cleanup must run after Program.Run returns, not concurrently on
//     Context().Done(), otherwise closing the windows races the final render
//     frames.
func tuiosSessionMiddleware() wish.Middleware {
	return func(next ssh.Handler) ssh.Handler {
		return func(sess ssh.Session) {
			_, windowChanges, active := sess.Pty()
			if !active {
				// No PTY requested, this shouldn't happen for TUIOS
				wish.Fatalln(sess, "No terminal. Run ssh with -t to request one.")
				return
			}

			out := &serialWriter{w: sess}
			model := buildSessionModel(sess, out)
			if model == nil {
				next(sess)
				return
			}

			// MakeOptions wires input/output/env for the session. The shared
			// list goes after it, because both carry a WithFilter and the
			// last one set wins (see app.ProgramOptions). WithOutput last
			// replaces the raw session writer with the serialized one shared
			// with the graphics path. This server never allocates a
			// server-side PTY (no ssh.AllocatePty), so the session itself is
			// always the right output to wrap.
			opts := append(bubbletea.MakeOptions(sess), app.ProgramOptions()...)
			opts = append(opts, tea.WithOutput(out))
			program := tea.NewProgram(model, opts...)

			ctx, cancel := context.WithCancel(sess.Context())
			go func() {
				for {
					select {
					case <-ctx.Done():
						program.Quit()
						return
					case w := <-windowChanges:
						program.Send(tea.WindowSizeMsg{Width: w.Width, Height: w.Height})
					}
				}
			}()

			if _, err := program.Run(); err != nil {
				log.Printf("SSH session program exited with error: %v", err)
			}
			// Kill force-stops the program if Quit was not enough and restores
			// the terminal state.
			program.Kill()
			cancel()

			// Tear down after the program has fully stopped. In daemon mode
			// this closes the daemon client, otherwise its read loop, socket,
			// and the daemon-side connState leak per connection. In ephemeral
			// mode it closes the local windows, otherwise each disconnect leaks
			// a shell process and its PTY inside this long-lived server.
			// Cleanup is idempotent. Running it here (not on Context().Done())
			// keeps it off the renderer's back while frames are still going
			// out.
			if o, ok := model.(*app.OS); ok {
				o.Cleanup()
			}
			next(sess)
		}
	}
}

// buildSessionModel creates a TUIOS instance for an SSH session. graphicsOut
// is the serialized session writer that kitty/sixel APC sequences are routed
// through; it must be the same writer the bubbletea program renders to.
func buildSessionModel(sshSession ssh.Session, graphicsOut io.Writer) tea.Model {
	pty, _, _ := sshSession.Pty()

	cfg := sshServerConfig
	if cfg == nil {
		cfg = &SSHServerConfig{Ephemeral: true}
	}

	// Detect the CLIENT terminal's graphics capabilities. The terminal that
	// must render forwarded images is the one the user connected from, reached
	// over this session, not the (often headless) server. The session gets
	// them as its own (app.OSOptions.Caps), which is what every consumer inside
	// it reads. The process global is not rewritten per connection: that was
	// last writer wins across clients, and StartSSHServer seeds it once.
	clientCaps := detectClientGraphics(sshSession)
	hostCaps := clientToHostCapabilities(clientCaps)

	// The accent picker's fallback labels describe what the terminal showing
	// the frame will do to each colour. Its default probe reads this process's
	// stdout and environment, which describe the server; pin the profile wish
	// derives for this client's renderer instead, so the labels and the frame
	// agree. This one is still a process global written per connection, so
	// with two clients the last to connect decides it.
	app.SetAccentColorProfile(colorprofile.Env(append(sshSession.Environ(), "TERM="+pty.Term)))

	// The kind says the rest: read-only config, no desktop, file-medium
	// graphics re-encoded for a terminal that cannot read server paths.
	opts := app.OSOptions{
		Client:        app.ClientSSH,
		ShowKeys:      cfg.ShowKeys,
		Width:         pty.Window.Width,
		Height:        pty.Window.Height,
		SSHSession:    sshSession,
		SSHIsLoopback: isLoopbackAddr(sshSession.RemoteAddr()),
		// Route kitty/sixel APC sequences to the SSH session so they reach the
		// client's terminal, via the serialized writer shared with the
		// bubbletea renderer so graphics and text writes never interleave on
		// the SSH channel. The passthrough enables itself only when the
		// client's detected capabilities (Caps, below) say the terminal can
		// render them, so this is a no-op for a plain client.
		GraphicsOutput: graphicsOut,
		// The terminal this client connected from, not the last one to connect.
		Caps: hostCaps,
	}

	// If ephemeral mode or daemon not available, use old behavior
	if cfg.Ephemeral {
		return served.NewModel(opts, cfg.Overrides)
	}

	version := cfg.Version
	if version == "" {
		version = "ssh-client"
	}

	// Try to connect to daemon. The CLIENT's capabilities go to the daemon,
	// which uses the cell pixel size to set each PTY's winsize pixel fields.
	// Those drive SGR-pixel mouse reporting (DEC 1016) and kitty geometry, so
	// they must describe the terminal the user connected from, not the server.
	daemonOpts := opts
	daemonOpts.SessionName = determineSessionName(sshSession, cfg)
	model, err := served.Attach(daemonOpts, cfg.Overrides, version, clientCaps, pickSSHSession)
	if err != nil {
		log.Printf("Warning: Failed to connect to daemon, using ephemeral mode: %v", err)
		return served.NewModel(opts, cfg.Overrides)
	}
	return model
}

// pickSSHSession is which session a connection with no name gets, and says
// so in the log when that was a choice between several.
func pickSSHSession(available []string) string {
	name := chooseSSHSession(available)
	if len(available) > 1 && name == DefaultSSHSessionName {
		log.Printf("Several sessions exist (%s) and the connection named none, so it gets %q",
			strings.Join(available, ", "), name)
	}
	return name
}

// determineSessionName determines which session to attach to based on SSH context
func determineSessionName(sshSession ssh.Session, cfg *SSHServerConfig) string {
	// Priority 1: Default session configured on server
	if cfg.DefaultSession != "" {
		return cfg.DefaultSession
	}

	// Priority 2: SSH username (if not generic)
	user := sshSession.User()
	if user != "" && user != "tuios" && user != "root" && user != "anonymous" {
		return user
	}

	// Priority 3: Parse command for "attach <session>" pattern
	cmd := sshSession.Command()
	if len(cmd) >= 2 && cmd[0] == "attach" {
		return cmd[1]
	}

	// Priority 4: Empty string = show session picker or use default
	return ""
}

// isLoopbackAddr reports whether an SSH connection arrived from the same
// machine (127.0.0.1, ::1, or a unix socket with no port). The native clipboard
// fallback is only safe for such sessions: the operator's clipboard IS the
// server's. A remote SSH peer's clipboard lives elsewhere, so it keeps the
// OSC 52 path.
func isLoopbackAddr(addr net.Addr) bool {
	if addr == nil {
		return false
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		// No host:port to split means a unix socket, which is always local.
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// DefaultSSHSessionName is the session a connection that named none gets when
// this machine's sessions cannot answer for it.
//
// It is the same answer tuios-web gives with "web", and for the same reason:
// what a server hands an unnamed connection has to be a decision rather than
// whatever the listing happened to return first.
const DefaultSSHSessionName = "ssh-session"

// chooseSSHSession picks the session for a connection that named none.
//
// The old rule took the first name the daemon listed when there were several,
// which is an ordering and not a choice. Two operators connecting to one
// server could land in different sessions from one another, or in a colleague's
// session, with nothing on screen saying which or why. The listing's order is
// not part of any contract, so the same connection could answer differently
// after a session was created or removed.
//
// The three cases are now three answers rather than two answers and a lottery:
//
//   - No sessions. There is nothing to choose between, so the connection gets
//     the default name and the daemon creates it. This is unchanged.
//   - One session. There is no ambiguity, so it gets that one. Also unchanged,
//     and the case that keeps a single-session server behaving the way someone
//     would expect.
//   - Several. The connection did not say, and neither did the person, so
//     picking one of theirs is a guess wearing a fact's clothes. It gets the
//     default name instead: deterministic, the same for every connection, and
//     nobody else's. Switching away from it is one keystroke.
//
// A username that maps to a session never reaches here; that mapping is
// determineSessionName and it is a real answer from the person connecting.
func chooseSSHSession(available []string) string {
	if len(available) == 1 {
		return available[0]
	}
	return DefaultSSHSessionName
}
