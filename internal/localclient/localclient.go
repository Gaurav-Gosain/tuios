// Package localclient runs the tuios client on a terminal: the one this
// process runs in, or one a test hands it (testing.md, blocker B1).
//
// cmd/tuios builds the client here for a standalone session and for an
// attach to a daemon. It keeps what only the command has: the checks of the
// terminal, the daemon connection and its errors, the signal handling, and
// the report at exit. A test runs the same client on a pipe.
//
// The terminal has the shape the SSH and web servers give theirs: input,
// output, environment, size, and a source of new sizes.
package localclient

import (
	"io"
	"log"
	"os"
	"runtime"
	"sync"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/termio"
)

// Terminal is where a client reads its keys and draws its frames.
type Terminal struct {
	// In is where the keys come from.
	In io.Reader
	// Out is where the frames go. A real file keeps the writer's direct
	// path. See app.PostRenderWriter.
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

	// stdio marks this process's own terminal, which Bubble Tea and the
	// screen ask for its size, profile and resizes themselves.
	stdio bool
}

// Size is a terminal size in cells.
type Size struct{ Width, Height int }

// Stdio is the terminal this process runs in.
func Stdio() Terminal {
	return Terminal{In: os.Stdin, Out: os.Stdout, stdio: true}
}

// LoadConfig loads the user config (falling back to defaults on error) and
// resolves the Settings the client starts from: the glyph environment getenv
// describes, the file, then the flags ov.
func LoadConfig(getenv func(string) string, ov config.Overrides) (*config.UserConfig, *config.Settings) {
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		log.Printf("Warning: Failed to load config, using defaults: %v", err)
		userConfig = config.DefaultConfig()
		// The log line above is wiped by the first frame. LoadWarnings reach
		// the TUI, so the person learns why none of the file applies.
		userConfig.LoadWarnings = append(userConfig.LoadWarnings,
			"tuios cannot load config.toml and uses the default settings: "+err.Error())
	}

	// What this terminal can draw, from its locale and TERM. It only decides
	// the glyphs when the config chose no set and --ascii-only was not given;
	// ApplyAppearanceConfig folds it in. See config.DetectGlyphEnv.
	env, why := config.DetectGlyphEnv(getenv)
	seed := config.DefaultSettings()
	seed.GlyphEnv = env
	if why != "" {
		log.Printf("glyphs: %s, so the chrome is drawn with %s glyphs unless a glyph set or --ascii-only is chosen", why, env)
	}

	// The file is the baseline and the flags win: the option flags are laid
	// over a copy of the file, and the rest are applied after it.
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

	term    Terminal
	started func(*tea.Program)
	ended   func()
	done    chan struct{}
	close   sync.Once
}

// New builds the client for opts on t. When opts.DaemonClient is set, state
// is the session the daemon handed over at the attach, and the client is
// wired to the daemon before the program exists.
//
// One writer reaches the terminal: frames, kitty and sixel sequences all
// serialize on it. Left out, the passthroughs open their own /dev/tty and
// nothing can order their writes against a frame.
func New(t Terminal, opts app.OSOptions, state *session.SessionState) *Client {
	prw := app.NewPostRenderWriter(t.Out)
	opts.GraphicsOutput = prw
	m := app.NewOS(opts)
	m.PostRenderWriter = prw
	// Sixel images follow the frame they sit on, in the same write.
	m.ConnectFrameWriter(prw)
	if opts.DaemonClient != nil {
		// Everything the daemon sends an attached client, queued for Update.
		// The same call the SSH server and tuios-web make.
		m.WireDaemonClient(opts.DaemonClient)
		m.RestoreAttachedSession(state)
	}

	c := &Client{OS: m, term: t, done: make(chan struct{})}
	// The shared list, then the options that are this transport's: the
	// writer every frame and every graphics sequence serialize on, and for a
	// terminal that is not this process's, its input, environment, profile
	// and size.
	popts, started, ended := screen(m, t, prw)
	var size atomic.Uint64
	if !t.stdio {
		storeSize(&size, t.Width, t.Height)
		popts = append(popts, app.WithStartSize(t.Width, t.Height), app.WithCurrentSize(func() (int, int) {
			v := size.Load()
			return int(v >> 32), int(uint32(v))
		}))
	}
	topts := append(app.ProgramOptions(popts...), tea.WithOutput(prw.Output()))
	if !t.stdio {
		topts = append(topts, tea.WithInput(t.In), tea.WithEnvironment(t.Env), tea.WithColorProfile(profileOf(t)))
	}
	p := tea.NewProgram(m, topts...)
	m.BindProgram(p)
	started(p)
	c.Program, c.started, c.ended = p, started, ended
	if t.Resize != nil {
		go c.forwardResizes(&size)
	}
	return c
}

// Run runs the program until it ends.
func (c *Client) Run() (tea.Model, error) {
	return c.Program.Run()
}

// Close stops reading new sizes and puts the terminal's screen back once the
// program has ended. It is safe to call more than once.
func (c *Client) Close() {
	c.close.Do(func() {
		close(c.done)
		c.ended()
	})
}

// Finish cleans up the model the program ended with and clears the OSC 7501
// records this client left on the terminal.
func (c *Client) Finish(final *app.OS) {
	final.Cleanup()
	_, _ = io.WriteString(c.term.Out, final.HostProgramStatusClear())
	_, _ = io.WriteString(c.term.Out, final.DnDHostClear())
}

// forwardResizes sends each new size of the terminal to the program. The size
// is stored before it is sent. See app.WithCurrentSize.
func (c *Client) forwardResizes(size *atomic.Uint64) {
	for {
		select {
		case <-c.done:
			return
		case s, ok := <-c.term.Resize:
			if !ok {
				return
			}
			storeSize(size, s.Width, s.Height)
			c.Program.Send(tea.WindowSizeMsg{Width: s.Width, Height: s.Height})
		}
	}
}

func storeSize(size *atomic.Uint64, w, h int) {
	size.Store(uint64(uint32(w))<<32 | uint64(uint32(h)))
}

// profileOf is the colour profile of a terminal that is not this process's.
func profileOf(t Terminal) colorprofile.Profile {
	if t.Profile != colorprofile.Unknown {
		return t.Profile
	}
	return colorprofile.Env(t.Env)
}

// screen gives the client a screen of its own when appearance.cell_frames
// asks for it (all, or TUIOS_CELL_FRAME=1): its frames are written as diffed
// cells and Bubble Tea runs without its renderer. It returns the program
// options that go with it, what to call once the program exists, and what to
// call when it has ended. Not offered on Windows.
func screen(m *app.OS, t Terminal, out io.Writer) (opts []app.ProgramOption, started func(*tea.Program), ended func()) {
	none := func(*tea.Program) {}
	if runtime.GOOS == "windows" || !m.Settings.CellFramesFor(false) {
		return nil, none, func() {}
	}
	if !t.stdio {
		m.UseScreen(out, t.Env, profileOf(t), t.Output)
		return []app.ProgramOption{app.WithOwnScreen(true)}, none, m.CloseScreen
	}
	if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
		return nil, none, func() {}
	}
	w, h, err := term.GetSize(os.Stdout.Fd())
	if err != nil {
		return nil, none, func() {}
	}
	env := os.Environ()
	m.UseScreen(out, env, colorprofile.Detect(os.Stdout, env), termio.Output{Raw: true, Console: os.Stdin})
	stop := func() {}
	return []app.ProgramOption{app.WithOwnScreen(true), app.WithStartSize(w, h)},
		func(p *tea.Program) { stop = m.WatchScreenResize(p.Send) },
		func() {
			stop()
			m.CloseScreen()
		}
}
