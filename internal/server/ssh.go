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
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"charm.land/ssh"
	"charm.land/wish/v2"
	"charm.land/wish/v2/bubbletea"
	"charm.land/wish/v2/logging"
	"github.com/charmbracelet/colorprofile"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/input"
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
	sshServerConfig = cfg

	// Compose frames in truecolor. composeFrame downsamples every frame
	// through lipgloss.Sprint, whose profile is detected from THIS process's
	// stdout and environment: a server logging to a file or running under a
	// service manager detects NoTTY and strips every colour from every frame
	// before the per-client profile ever sees it, for every client at once.
	// Pinning truecolor here leaves downsampling to the bubbletea renderer,
	// which wish configures per connection from the client's own TERM.
	// tuios-web pins the same global for the same reason.
	lipgloss.Writer.Profile = colorprofile.TrueColor

	// Apply the user config's appearance globals once, at first server startup
	// and single-threaded, so every per-connection session shares a consistent
	// view of them. LoadUserConfig is pure and NewOS no longer re-applies per
	// connection, so this replaces the old per-connection global writes that
	// raced other sessions' render loops.
	applyAppearanceOnce.Do(func() {
		if userConfig, err := config.LoadUserConfig(); err == nil {
			config.ApplyAppearanceConfig(userConfig)
		}
		// Flags over file, the same order loadAndApplyConfig gives every
		// other entrypoint.
		config.ApplyOverrides(cfg.Overrides)
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

	// If using daemon mode, ensure daemon is running
	if !cfg.Ephemeral {
		if err := session.EnsureDaemonRunning(); err != nil {
			log.Printf("Warning: Failed to start daemon, falling back to ephemeral mode: %v", err)
			cfg.Ephemeral = true
		}
	}

	// Create SSH server with middleware
	server, err := wish.NewServer(
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
	)
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

// shortID returns the first 8 characters of an id for logging, or the whole id
// when it is shorter, so a non-UUID id cannot panic the log call.
func shortID(id string) string {
	if len(id) < 8 {
		return id
	}
	return id[:8]
}

// recoverMiddleware wraps a session handler so a panic in it (or any inner
// middleware) is recovered, logged, and confined to that one session. Bubble
// Tea already recovers panics inside its own program loop and returns from
// Run; this is the backstop for everything outside that loop - session setup,
// capability detection, the daemon connect/restore path - so a single bad
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
// connection, which is exactly how a kitty graphics flood used to kill the
// session. Every writer to the session must go through one shared
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
				wish.Fatalln(sess, "no active terminal, skipping")
				return
			}

			out := &serialWriter{w: sess}
			model, opts := buildSessionModel(sess, out)
			if model == nil {
				next(sess)
				return
			}

			// MakeOptions wires input/output/env for the session; WithOutput
			// afterwards replaces the raw session writer with the serialized
			// one shared with the graphics path. This server never allocates a
			// server-side PTY (no ssh.AllocatePty), so the session itself is
			// always the right output to wrap.
			opts = append(opts, bubbletea.MakeOptions(sess)...)
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
func buildSessionModel(sshSession ssh.Session, graphicsOut io.Writer) (tea.Model, []tea.ProgramOption) {
	pty, _, _ := sshSession.Pty()

	cfg := sshServerConfig
	if cfg == nil {
		cfg = &SSHServerConfig{Ephemeral: true}
	}

	// Detect the CLIENT terminal's graphics capabilities. The terminal that
	// must render forwarded images is the one the user connected from, reached
	// over this session, not the (often headless) server. Install them as the
	// process host capabilities so the image cell math and cell-size lookups
	// that read GetHostCapabilities report the client, not the server.
	clientCaps := detectClientGraphics(sshSession)
	app.SetClientCapabilities(clientToHostCapabilities(clientCaps))

	// Determine session name from SSH context
	sessionName := determineSessionName(sshSession, cfg)

	// If ephemeral mode or daemon not available, use old behavior
	if cfg.Ephemeral {
		return createEphemeralTUIOSInstance(sshSession, graphicsOut, pty.Window.Width, pty.Window.Height)
	}

	// Try to connect to daemon
	model, opts, err := createDaemonTUIOSInstance(sshSession, graphicsOut, sessionName, pty.Window.Width, pty.Window.Height, cfg, clientCaps)
	if err != nil {
		log.Printf("Warning: Failed to connect to daemon, using ephemeral mode: %v", err)
		return createEphemeralTUIOSInstance(sshSession, graphicsOut, pty.Window.Width, pty.Window.Height)
	}
	return model, opts
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

// createEphemeralTUIOSInstance creates a standalone TUIOS instance (old behavior)
func createEphemeralTUIOSInstance(sshSession ssh.Session, graphicsOut io.Writer, width, height int) (tea.Model, []tea.ProgramOption) {
	// Load user configuration and create keybind registry
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		log.Printf("Warning: Failed to load config for SSH session, using defaults: %v", err)
		userConfig = config.DefaultConfig()
	}
	keybindRegistry := config.NewKeybindRegistry(userConfig)

	// Set up the input handler
	app.SetInputHandler(input.HandleInput)

	tuiosInstance := app.NewOS(app.OSOptions{
		KeybindRegistry: keybindRegistry,
		UserConfig:      userConfig,
		Width:           width,
		Height:          height,
		IsSSHMode:       true,
		SSHSession:      sshSession,
		// The config file belongs to the server operator, and several SSH
		// clients each hold a stale snapshot of it; the same reasoning that
		// makes a web-served session read-only (see OSOptions) applies here.
		// Settings still apply live, they are just not written back.
		ConfigReadOnly: true,
		// Route kitty/sixel APC sequences to the SSH session so they reach the
		// client's terminal, via the serialized writer shared with the
		// bubbletea renderer so graphics and text writes never interleave on
		// the SSH channel. The passthrough enables itself only when the
		// client's detected capabilities (installed via SetClientCapabilities)
		// say the terminal can render them, so this is a no-op for a plain
		// client. RemoteClient forces file-medium transmissions to be
		// re-encoded as direct data, since the client cannot read server paths.
		GraphicsOutput:       graphicsOut,
		GraphicsRemoteClient: true,
	})

	return tuiosInstance, []tea.ProgramOption{
		tea.WithFPS(config.MaxFPSCap),
	}
}

// createDaemonTUIOSInstance creates a TUIOS instance connected to the daemon
func createDaemonTUIOSInstance(sshSession ssh.Session, graphicsOut io.Writer, sessionName string, width, height int, cfg *SSHServerConfig, clientCaps *session.ClientCapabilities) (tea.Model, []tea.ProgramOption, error) {
	// Connect to daemon
	client := session.NewTUIClient()
	version := cfg.Version
	if version == "" {
		version = "ssh-client"
	}

	// Forward the CLIENT's capabilities to the daemon. The daemon uses the cell
	// pixel size to set each PTY's winsize pixel fields, which drive SGR-pixel
	// mouse reporting (DEC 1016) and kitty geometry. These must describe the
	// terminal the user connected from, not the server.
	if err := client.ConnectWithCapabilities(version, width, height, clientCaps); err != nil {
		return nil, nil, fmt.Errorf("failed to connect to daemon: %w", err)
	}

	// If no session name specified, show picker or get default
	if sessionName == "" {
		availableSessions := client.AvailableSessionNames()
		if len(availableSessions) == 0 {
			// No sessions exist, create a new one
			sessionName = "ssh-session"
		} else if len(availableSessions) == 1 {
			// Only one session, use it
			sessionName = availableSessions[0]
		} else {
			// Multiple sessions - use the first one for now
			// TODO: Could run session picker here, but that requires a different flow
			sessionName = availableSessions[0]
			log.Printf("Multiple sessions available, attaching to: %s", sessionName)
		}
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
		log.Printf("Warning: Failed to load config for SSH session, using defaults: %v", err)
		userConfig = config.DefaultConfig()
	}
	keybindRegistry := config.NewKeybindRegistry(userConfig)

	// Set up the input handler
	app.SetInputHandler(input.HandleInput)

	// Create TUIOS instance connected to daemon
	tuiosInstance := app.NewOS(app.OSOptions{
		KeybindRegistry: keybindRegistry,
		UserConfig:      userConfig,
		Width:           width,
		Height:          height,
		IsSSHMode:       true,
		SSHSession:      sshSession,
		// Read-only for the same reason as the ephemeral path above.
		ConfigReadOnly:  true,
		IsDaemonSession: true,
		DaemonClient:    client,
		SessionName:     sessionName,
		// Route graphics to the SSH session (through the serialized writer
		// shared with the renderer) so kitty/sixel APCs reach the client's
		// terminal, and re-encode file-medium transmissions as direct data
		// since the client cannot read server-local paths.
		GraphicsOutput:       graphicsOut,
		GraphicsRemoteClient: true,
	})

	// Restore state from daemon if available
	if state != nil && len(state.Windows) > 0 {
		log.Printf("[SSH] Restoring %d windows from session state", len(state.Windows))
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
		log.Printf("[SSH] Received state sync: trigger=%s, source=%s", triggerType, shortID(sourceID))
		// Send state to channel for processing in Bubble Tea event loop
		// This ensures thread-safe access to m.Windows
		if m.QueueStateSync(state) {
			log.Printf("[SSH] StateSyncChan full, superseded the queued snapshot")
		}
	})

	// Handle client join notifications via channel (thread-safe)
	client.OnClientJoined(func(clientID string, clientCount int, width, height int) {
		log.Printf("[SSH] Client joined: %s (total: %d, size: %dx%d)", shortID(clientID), clientCount, width, height)
		if m.ClientEventChan != nil {
			select {
			case m.ClientEventChan <- app.ClientEvent{Type: "joined", ClientID: clientID, ClientCount: clientCount, Width: width, Height: height}:
			default:
				log.Printf("[SSH] Warning: ClientEventChan full, dropping client joined event")
			}
		}
	})

	// Handle client leave notifications via channel (thread-safe)
	client.OnClientLeft(func(clientID string, clientCount int) {
		log.Printf("[SSH] Client left: %s (remaining: %d)", shortID(clientID), clientCount)
		if m.ClientEventChan != nil {
			select {
			case m.ClientEventChan <- app.ClientEvent{Type: "left", ClientID: clientID, ClientCount: clientCount}:
			default:
				log.Printf("[SSH] Warning: ClientEventChan full, dropping client left event")
			}
		}
	})

	// Handle session resize (min of all clients). The callback runs on the daemon
	// read-loop goroutine, so the actual geometry mutation (TileAllWindows,
	// emulator resizes) must happen in Update; route it through the event channel.
	client.OnSessionResize(func(width, height, clientCount int) {
		log.Printf("[SSH] Session resize: %dx%d (clients: %d)", width, height, clientCount)
		if m.ClientEventChan != nil {
			select {
			case m.ClientEventChan <- app.ClientEvent{Type: "resize", ClientCount: clientCount, Width: width, Height: height}:
			default:
				log.Printf("[SSH] Warning: ClientEventChan full, dropping session resize event")
			}
		}
	})
}

// Window is an alias for terminal.Window for use in this package
type Window = terminal.Window
