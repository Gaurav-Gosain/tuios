// Package tutor runs tuios tutor: the tour of tuios.dev/learn, in the
// person's own terminal.
//
// It runs the same stack as the browser build, natively and in this one
// process: the real app.OS in Learn mode, with the internal/webshell fake
// shell in every pane, and internal/learn running the lessons. There is no
// daemon and no socket, and no real process starts. The tutor reads none of
// the person's config. It moves the home, the XDG folders, TMPDIR and PATH
// into a temp dir for the rest of the process, clears every TUIOS_ variable,
// and takes the temp dir away when it ends, by a key or by a signal. The one
// file it writes outside that dir is the progress file,
// $XDG_STATE_HOME/tuios/tutor.json.
//
// The screen is tuios with a card under it: the step, its keys lit as they
// are pressed, the note or the hint, and the tutor's own keys (F2 menu, F3
// show me, F4 skip). The quit keys of tuios open the tutor's menu.
package tutor

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/adrg/xdg"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
	"github.com/charmbracelet/x/xpty"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/learn"
	"github.com/Gaurav-Gosain/tuios/internal/learn/lessons"
	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
	"github.com/Gaurav-Gosain/tuios/internal/serverscope"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/webshell"
)

// Tracks are the tracks the tutor offers, in order. A track is listed here
// once its steps read right in a terminal.
var Tracks = []string{"basics"}

// Options are the tutor's command line.
type Options struct {
	// Track opens this track at once, past the menu.
	Track string
	// ASCII draws the tutor's own glyphs in ASCII.
	ASCII bool
}

// ProgressPath is where the tutor keeps the person's progress.
func ProgressPath() string {
	// Progress belongs to the person, not to a named server.
	_ = serverscope.Apply("")
	return filepath.Join(xdg.StateHome, "tuios", "tutor.json")
}

// Reset removes the progress file.
func Reset() error { return resetProgress(ProgressPath()) }

// List writes the tracks the tutor offers.
func List(w io.Writer) error {
	f, err := lessons.Load()
	if err != nil {
		return err
	}
	for _, id := range Tracks {
		t := f.Find(id)
		if t == nil {
			continue
		}
		if _, err := fmt.Fprintf(w, "%-12s %-20s %d steps, %d min\n", t.ID, t.Title, len(t.Steps), t.Minutes); err != nil {
			return err
		}
	}
	return nil
}

// Run runs the tutor on the terminal of stdin and stdout until the person
// leaves.
func Run(opts Options) error {
	if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
		return errors.New("tuios tutor needs a terminal. Run it in a terminal window, not in a pipe")
	}
	file, err := lessons.Load()
	if err != nil {
		return err
	}
	var tracks []*lessons.Track
	for _, id := range Tracks {
		if t := file.Find(id); t != nil && t.On(lessons.SurfaceTerminal) {
			tracks = append(tracks, t)
		}
	}
	var first *lessons.Track
	if opts.Track != "" {
		for _, t := range tracks {
			if t.ID == opts.Track {
				first = t
			}
		}
		if first == nil {
			return fmt.Errorf("the tutor has no track %q. Run tuios tutor --list to see the tracks", opts.Track)
		}
	}

	// Read before the sandbox moves them.
	progressPath := ProgressPath()
	nested := os.Getenv("TUIOS_PANE_ID") != "" || os.Getenv("TUIOS_WINDOW_ID") != "" || os.Getenv("TUIOS_SESSION") != ""
	box, err := enterSandbox()
	if err != nil {
		return fmt.Errorf("make the tutor's temp dir: %w", err)
	}
	defer box.remove()
	store := loadProgress(progressPath)

	// Every pane gets an in-memory pty running the fake shell. No process
	// starts.
	ptyspawn.NewGuestPty = func(width, height int) (xpty.Pty, error) {
		return webshell.NewPty(width, height), nil
	}
	ptyspawn.GuestDir = webshell.IsDir
	webshell.SetFlavorTerminal()

	w, h, err := term.GetSize(os.Stdout.Fd())
	if err != nil || w <= 0 || h <= 0 {
		w, h = 80, 24
	}
	profile := colorprofile.Detect(os.Stdout, os.Environ())

	// The shipped defaults, the screen a person sees after installing tuios.
	cfg := learn.Config()
	app.SetInputHandler(input.HandleInput)
	config.ApplyAppearanceConfig(cfg, &config.Global)
	seed := config.AppearanceFrom(cfg, config.Overrides{ASCIIOnly: opts.ASCII})
	o := app.NewOS(app.OSOptions{
		Client:          app.ClientLocal,
		LearnMode:       true,
		GuestApps:       learn.LauncherApps(),
		ConfigReadOnly:  true,
		KeybindRegistry: config.NewKeybindRegistry(cfg),
		UserConfig:      cfg,
		Settings:        &seed,
		Width:           w,
		Height:          max(h-4, 1),
		Caps: &app.HostCapabilities{
			TrueColor:    profile == colorprofile.TrueColor,
			TerminalName: "tuios-tutor",
		},
	})
	o.LearnNotes = terminalNotes()

	in := newInput(os.Stdin)
	var prog *tea.Program
	queue := &eventQueue{}
	m := &Model{file: file, tracks: tracks, store: store, queue: queue, ascii: opts.ASCII || seed.UseASCIIOnly, w: w, h: h}
	m.lessons, err = learn.NewLessons(lessons.SurfaceTerminal, learn.LessonHooks{
		Emit: queue.push,
		// The lesson's own goroutines call this, never Update, so it may
		// block, and two calls reach the program in order.
		Send:  func(msg tea.Msg) { prog.Send(msg) },
		Input: func(s string) { in.inject([]byte(s)) },
		State: func() map[string]any { return m.tour.State().ToMap() },
	})
	if err != nil {
		return err
	}
	m.tour = learn.New(o, func(e learn.Event) {
		m.lessons.Observe(e)
		if e.Type == learn.EventAction {
			queue.push(e)
		}
	}, func(msg tea.Msg) { go prog.Send(msg) })
	switch {
	case nested:
		m.scr = scrNested
	case first != nil:
		m.scr = scrLesson
	default:
		m.scr = scrMenu
	}

	// No WithWindowSize: stdout is the terminal, and Bubble Tea reads the
	// size off it at start and on every resize.
	prog = tea.NewProgram(m, append(app.ProgramOptions(),
		tea.WithInput(in),
		tea.WithOutput(os.Stdout),
		// Last, so it replaces the filter ProgramOptions set: that one only
		// knows *app.OS.
		tea.WithFilter(filter),
	)...)
	queue.nudge = func() { go prog.Send(drainMsg{}) }
	o.BindProgram(prog)
	if first != nil && !nested {
		m.start(first)
	}

	// A signal ends the tutor the way Leave does. If the program cannot
	// end, the temp dir still goes, and so does the process.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)
	go func() {
		if _, ok := <-sigs; !ok {
			return
		}
		m.leaving.Store(true)
		prog.Send(leaveMsg{})
		time.Sleep(2 * time.Second)
		box.remove()
		os.Exit(1)
	}()

	_, err = prog.Run()
	for _, win := range m.tour.OS.Windows {
		win.Close()
	}
	box.remove()
	terminal.ResetTerminal()
	if err != nil && !errors.Is(err, tea.ErrProgramKilled) {
		return fmt.Errorf("tutor: %w", err)
	}
	return nil
}

// terminalNotes rewrites Learn mode's notes, which talk about a browser tab.
// The quit note shows nothing: a quit opens the tutor's menu instead.
func terminalNotes() map[string]string {
	out := map[string]string{}
	for _, note := range app.LearnUnavailableActions() {
		switch note {
		case "No need to quit here. Just close the tab when you're done.":
			out[note] = ""
		case "Paste with your browser here: Cmd+V or Ctrl+Shift+V.":
			out[note] = "Paste with your terminal's own paste key."
		case "The demo does not change files or use the clipboard.":
			out[note] = "The tutor does not change files or use the clipboard."
		case "Sessions are not in the browser demo. Install tuios for them.":
			out[note] = "Sessions are not in the tutor. Run tuios after the tutor for them."
		default:
			out[note] = strings.NewReplacer("browser demo", "tutor", "the demo", "the tutor").Replace(note)
		}
	}
	return out
}
