// Package server provides SSH server functionality for TUIOS.
package server

import (
	"context"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/ssh"
	"charm.land/wish/v2"
	"charm.land/wish/v2/bubbletea"
	"charm.land/wish/v2/logging"
	"github.com/charmbracelet/colorprofile"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/served"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/termio"
)

// SSHServerConfig holds configuration for the SSH server.
type SSHServerConfig struct {
	imageSymbols string

	Host           string
	Port           string
	KeyPath        string
	DefaultSession string // If set, all connections attach to this session
	Ephemeral      bool   // If true, don't use daemon (old behavior)
	Version        string // For daemon handshake
	// AuthorizedKeysPath names the file of public keys allowed to connect.
	// Empty reads tuios/authorized_keys under the XDG config home, and only
	// that: ~/.ssh/authorized_keys is read only when named here. See auth.go.
	AuthorizedKeysPath string
	// ShowKeys turns the key display overlay on in every served session. It is
	// the --show-keys flag `tuios ssh` registers with the rest of the interface
	// flags.
	ShowKeys bool
	// NoAuth accepts every connection without checking who it is. It is the
	// opt-out that lets a non-loopback bind run with no authorized keys, and
	// the way back in for an operator whose key file locked them out.
	NoAuth bool
	// HandshakeTimeout, IdleTimeout, MaxHandshakes and MaxSessions are the
	// command line limits. Each one that is set replaces the value of the
	// [ssh] section of the config file. Nil keeps the file's value.
	HandshakeTimeout *time.Duration
	IdleTimeout      *time.Duration
	MaxHandshakes    *int
	MaxSessions      *int
	// Overrides carries the interface CLI flags, layered over the appearance
	// baseline inside the same once-guarded application so flags win over the
	// file. The zero value applies nothing.
	Overrides config.Overrides
}

var sshServerConfig *SSHServerConfig

var applyAppearanceOnce sync.Once

// StartSSHServer initializes and runs the SSH server
func StartSSHServer(ctx context.Context, cfg *SSHServerConfig) error {
	authPlan, err := PlanSSHAuth(cfg.Host, cfg.AuthorizedKeysPath, cfg.NoAuth)
	if err != nil {
		return err
	}

	sshServerConfig = cfg

	userConfig, err := config.LoadUserConfig()
	if err != nil {
		log.Printf("Warning: Failed to load config for the SSH server, using defaults: %v", err)
		userConfig = nil
	}

	applyAppearanceOnce.Do(func() {
		terminal.SetHeadlessGuestTerm("xterm-256color", "truecolor")

		app.SetClientCapabilities(&app.HostCapabilities{TrueColor: true})

		cfg.imageSymbols = config.Resolve(userConfig, cfg.Overrides).ImageSymbols
	})

	var hostKeyPath string
	if cfg.KeyPath != "" {
		hostKeyPath = cfg.KeyPath
	} else {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return errfmt.Errorf("failed to get user home directory: %w", err)
		}
		hostKeyPath = filepath.Join(homeDir, ".ssh", "tuios_host_key")
	}

	if !cfg.Ephemeral {
		daemonCfg := session.DaemonConfigFromUser(userConfig)
		if err := session.EnsureDaemonRunningWith(cfg.Version, daemonCfg); err != nil {
			log.Printf("Warning: Failed to start daemon, falling back to ephemeral mode: %v", err)
			cfg.Ephemeral = true
		}
	}

	limits := sshLimits(userConfig, cfg)
	log.Printf("SSH limits: handshake_timeout %s, idle_timeout %s, max_handshakes %s, max_sessions %s",
		limitDuration(limits.HandshakeTimeout), limitDuration(limits.IdleTimeout),
		limitCount(limits.MaxHandshakes), limitCount(limits.MaxSessions))

	opts := []ssh.Option{
		wish.WithAddress(net.JoinHostPort(cfg.Host, cfg.Port)),
		wish.WithHostKeyPath(hostKeyPath),
		wish.WithMiddleware(
			tuiosSessionMiddleware(),
			sessionLimitMiddleware(limits.MaxSessions),
			logging.Middleware(),
			recoverMiddleware(),
		),
	}

	if authPlan.Authenticated() {
		opts = append(opts, wish.WithPublicKeyAuth(publicKeyHandler(authPlan.Keys.Path)))
		log.Printf("SSH authentication is on. %s from %s", plural.Count(len(authPlan.Keys.Keys), "key"), authPlan.Keys.Path)
	} else {
		log.Print(authPlan.Warning)
	}

	opts = append(opts, limitOptions(limits)...)

	server, err := wish.NewServer(opts...)
	if err != nil {
		return errfmt.Errorf("failed to create SSH server: %w", err)
	}

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

	<-ctx.Done()

	log.Println("Shutting down SSH server...")
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	return server.Shutdown(shutdownCtx)
}

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

type serialWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *serialWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

func tuiosSessionMiddleware() wish.Middleware {
	return func(next ssh.Handler) ssh.Handler {
		return func(sess ssh.Session) {
			pty, windowChanges, active := sess.Pty()
			if !active {
				wish.Fatalln(sess, "No terminal. Run ssh with -t to request one.")
				return
			}

			out := &serialWriter{w: sess}
			frames := app.NewFrameHookWriter(out)
			model, err := buildSessionModel(sess, out)
			if err != nil {
				wish.Fatalln(sess, err.Error())
				return
			}

			model.ConnectFrameWriter(frames)
			var size atomic.Uint64
			storeSize := func(w, h int) { size.Store(uint64(uint32(w))<<32 | uint64(uint32(h))) }
			storeSize(pty.Window.Width, pty.Window.Height)
			current := app.WithCurrentSize(func() (int, int) {
				v := size.Load()
				return int(v >> 32), int(uint32(v))
			})
			ownScreen := model.Settings.CellFramesFor(true)
			if ownScreen {
				env := append(sess.Environ(), "TERM="+pty.Term)
				model.UseScreen(frames, env, colorprofile.Env(env), termio.Output{Raw: false})
			}
			opts := append(bubbletea.MakeOptions(sess), app.ProgramOptions(current, app.WithOwnScreen(ownScreen))...)
			opts = append(opts, tea.WithOutput(frames))
			program := tea.NewProgram(model, opts...)
			model.BindProgram(program)

			ctx, cancel := context.WithCancel(sess.Context())
			go func() {
				for {
					select {
					case <-ctx.Done():
						program.Quit()
						return
					case w := <-windowChanges:
						storeSize(w.Width, w.Height)
						program.Send(tea.WindowSizeMsg{Width: w.Width, Height: w.Height})
					}
				}
			}()

			if _, err := program.Run(); err != nil {
				log.Printf("SSH session program exited with error: %v", err)
			}
			program.Kill()
			cancel()
			model.CloseScreen()
			if seq := model.HostProgramStatusClear(); seq != "" {
				_, _ = io.WriteString(sess, seq)
			}

			model.Cleanup()
			next(sess)
		}
	}
}

func buildSessionModel(sshSession ssh.Session, graphicsOut io.Writer) (*app.OS, error) {
	pty, _, _ := sshSession.Pty()

	cfg := sshServerConfig
	if cfg == nil {
		cfg = &SSHServerConfig{Ephemeral: true}
	}

	clientCaps := detectClientGraphics(sshSession, cfg.imageSymbols)
	hostCaps := clientToHostCapabilities(clientCaps)
	if env := parseEnviron(sshSession.Environ()); (env["TUIOS_SIXEL_GRAPHICS"] == "0" || env["TUIOS_SIXEL_GRAPHICS"] == "1") && hostCaps != nil {
		hostCaps.SixelPinned = true
	}

	app.SetAccentColorProfile(colorprofile.Env(append(sshSession.Environ(), "TERM="+pty.Term)))

	opts := app.OSOptions{
		Client:         app.ClientSSH,
		ShowKeys:       cfg.ShowKeys,
		Width:          pty.Window.Width,
		Height:         pty.Window.Height,
		SSHSession:     sshSession,
		SSHIsLoopback:  isLoopbackAddr(sshSession.RemoteAddr()),
		GraphicsOutput: graphicsOut,
		Caps:           hostCaps,
	}

	if cfg.Ephemeral {
		return served.NewModel(opts, cfg.Overrides), nil
	}

	version := cfg.Version
	if version == "" {
		version = "ssh-client"
	}

	daemonOpts := opts
	daemonOpts.SessionName = determineSessionName(sshSession, cfg)
	model, err := served.Attach(daemonOpts, cfg.Overrides, version, clientCaps, pickSSHSession)
	if err != nil {
		if nested, ok := session.AsNestedAttach(err); ok {
			return nil, nested
		}
		log.Printf("Warning: Failed to connect to daemon, using ephemeral mode: %v", err)
		return served.NewModel(opts, cfg.Overrides), nil
	}
	return model, nil
}

func pickSSHSession(available []string) string {
	name := chooseSSHSession(available)
	if len(available) > 1 && name == DefaultSSHSessionName {
		log.Printf("Several sessions exist (%s) and the connection named none, so it gets %q",
			strings.Join(available, ", "), name)
	}
	return name
}

func determineSessionName(sshSession ssh.Session, cfg *SSHServerConfig) string {
	if cfg.DefaultSession != "" {
		return cfg.DefaultSession
	}

	user := sshSession.User()
	if user != "" && user != "tuios" && user != "root" && user != "anonymous" {
		return user
	}

	cmd := sshSession.Command()
	if len(cmd) >= 2 && cmd[0] == "attach" {
		return cmd[1]
	}

	return ""
}

func isLoopbackAddr(addr net.Addr) bool {
	if addr == nil {
		return false
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
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

func chooseSSHSession(available []string) string {
	if len(available) == 1 {
		return available[0]
	}
	return DefaultSSHSessionName
}
