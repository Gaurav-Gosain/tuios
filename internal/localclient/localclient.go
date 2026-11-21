// Package localclient runs the tuios client on a terminal: the one this
// process runs in, or one a test hands it (testing.md, blocker B1).
//
// cmd/tuios builds the client here for a standalone session and for an
// attach to a daemon. It keeps what only the command has: the checks of the
// terminal, the daemon connection and its errors, the signal handling, and
// the report at exit. A test runs the same client on a Pipe.
package localclient

import (
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/termio"
)

// Terminal is where a client reads its keys and draws its frames: Stdio, or
// a Pipe.
//
// It is an interface so the binary links only the terminal it uses. Pipe is
// for tests, and nothing in cmd/tuios makes one, so its code is not in the
// binary.
type Terminal interface {
	out() io.Writer
	attach(m *app.OS, prw *app.PostRenderWriter) (opts []app.ProgramOption, topts []tea.ProgramOption, started func(*tea.Program), ended func())
}

// Stdio is the terminal this process runs in.
func Stdio() Terminal { return &stdio{} }

type stdio struct{}

func (*stdio) out() io.Writer { return os.Stdout }

func (*stdio) attach(m *app.OS, prw *app.PostRenderWriter) ([]app.ProgramOption, []tea.ProgramOption, func(*tea.Program), func()) {
	none := func(*tea.Program) {}
	if runtime.GOOS == "windows" || !m.Settings.CellFramesFor(false) ||
		!term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
		return nil, nil, none, func() {}
	}
	w, h, err := term.GetSize(os.Stdout.Fd())
	if err != nil {
		return nil, nil, none, func() {}
	}
	env := os.Environ()
	m.UseScreen(prw, env, colorprofile.Detect(os.Stdout, env), termio.Output{Raw: true, Console: os.Stdin})
	stop := func() {}
	return []app.ProgramOption{app.WithOwnScreen(true), app.WithStartSize(w, h)}, nil,
		func(p *tea.Program) { stop = m.WatchScreenResize(p.Send) },
		func() {
			stop()
			m.CloseScreen()
		}
}

// Pipe is a terminal that is not this process's, such as a test's pipe. It
// has the shape the SSH and web servers give theirs: input, output,
// environment, size, and a source of new sizes.
type Pipe struct {
	// In is where the keys come from.
	In io.Reader
	// Out is where the frames go.
	Out io.Writer
	// Env is the terminal's environment: TERM and COLORTERM decide which
	// sequences the frames may use.
	Env []string
	// Width and Height are the terminal's size when the client starts.
	Width, Height int
	// Resize carries each new size. The client stops reading it when its
	// program ends.
	Resize <-chan Size
	// Profile is the terminal's colour profile. Unknown takes it from Env.
	Profile colorprofile.Profile
	// Output says how the bytes reach the terminal. See termio.Output.
	Output termio.Output
}

// Size is a terminal size in cells.
type Size struct{ Width, Height int }

func (t Pipe) out() io.Writer { return t.Out }

func (t Pipe) attach(m *app.OS, prw *app.PostRenderWriter) ([]app.ProgramOption, []tea.ProgramOption, func(*tea.Program), func()) {
	profile := t.Profile
	if profile == colorprofile.Unknown {
		profile = colorprofile.Env(t.Env)
	}
	var size atomic.Uint64
	store := func(w, h int) { size.Store(uint64(uint32(w))<<32 | uint64(uint32(h))) }
	store(t.Width, t.Height)
	opts := []app.ProgramOption{app.WithStartSize(t.Width, t.Height), app.WithCurrentSize(func() (int, int) {
		v := size.Load()
		return int(v >> 32), int(uint32(v))
	})}
	own := runtime.GOOS != "windows" && m.Settings.CellFramesFor(false)
	if own {
		m.UseScreen(prw, t.Env, profile, t.Output)
		opts = append(opts, app.WithOwnScreen(true))
	}
	topts := []tea.ProgramOption{tea.WithInput(t.In), tea.WithEnvironment(t.Env), tea.WithColorProfile(profile)}
	done := make(chan struct{})
	started := func(p *tea.Program) {
		if t.Resize == nil {
			return
		}
		go func() {
			for {
				select {
				case <-done:
					return
				case s, ok := <-t.Resize:
					if !ok {
						return
					}
					store(s.Width, s.Height)
					p.Send(tea.WindowSizeMsg{Width: s.Width, Height: s.Height})
				}
			}
		}()
	}
	ended := func() {
		close(done)
		if own {
			m.CloseScreen()
		}
	}
	return opts, topts, started, ended
}

// LoadConfig loads the user config (falling back to defaults on error) and
// resolves the Settings the client starts from: the glyph environment getenv
// describes, the file, then the flags ov.
func LoadConfig(getenv func(string) string, ov config.Overrides) (*config.UserConfig, *config.Settings) {
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		log.Printf("Warning: Failed to load config, using defaults: %v", err)
		userConfig = config.DefaultConfig()
		userConfig.LoadWarnings = append(userConfig.LoadWarnings,
			fmt.Sprintf("tuios cannot load config.toml and uses the default settings: %v", err))
	}

	env, why := config.DetectGlyphEnv(getenv)
	seed := config.DefaultSettings()
	seed.GlyphEnv = env
	if why != "" {
		log.Printf("glyphs: %s, so the chrome is drawn with %s glyphs unless a glyph set or --ascii-only is chosen", why, env)
	}

	settings := config.ResolveFrom(seed, userConfig, ov)
	return userConfig, &settings
}

// Client is a client built on a terminal, ready to run.
type Client struct {
	// OS is the model the program runs.
	OS *app.OS
	// Program is the Bubble Tea program. The caller may arm its signal
	// handling on it before Run.
	Program *tea.Program

	out   io.Writer
	ended func()
}

// New builds the client for opts on t. When opts.DaemonClient is set, state
// is the session the daemon handed over at the attach, and the client is
// wired to the daemon before the program exists.
//
// One writer reaches the terminal: frames, kitty and sixel sequences all
// serialize on it. Left out, the passthroughs open their own /dev/tty and
// nothing can order their writes against a frame.
func New(t Terminal, opts app.OSOptions, state *session.SessionState) *Client {
	out := t.out()
	prw := app.NewPostRenderWriter(out)
	opts.GraphicsOutput = prw
	m := app.NewOS(opts)
	m.PostRenderWriter = prw
	m.ConnectFrameWriter(prw)
	if opts.DaemonClient != nil {
		m.WireDaemonClient(opts.DaemonClient)
		m.RestoreAttachedSession(state)
	}

	popts, topts, started, ended := t.attach(m, prw)
	p := tea.NewProgram(m, append(append(app.ProgramOptions(popts...), tea.WithOutput(prw.Output())), topts...)...)
	m.BindProgram(p)
	started(p)
	return &Client{OS: m, Program: p, out: out, ended: ended}
}

// Run runs the program until it ends.
func (c *Client) Run() (tea.Model, error) {
	return c.Program.Run()
}

// Close puts the terminal's screen back once the program has ended, and stops
// reading new sizes. Call it once.
func (c *Client) Close() { c.ended() }

// Finish cleans up the model the program ended with and clears the OSC 7501
// records this client left on the terminal.
func (c *Client) Finish(final *app.OS) {
	final.Cleanup()
	_, _ = io.WriteString(c.out, final.HostProgramStatusClear())
	_, _ = io.WriteString(c.out, final.DnDHostClear())
}
