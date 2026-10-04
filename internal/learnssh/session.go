package learnssh

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/xpty"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/learn"
	"github.com/Gaurav-Gosain/tuios/internal/learn/lessons"
	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/webshell"
)

// RunSession is the child process for one SSH session. in carries the
// reader's keys and mouse, out goes to their terminal, and ctlIn and ctlOut
// are the control pipes to the server. The first control message must be
// init.
//
// It locks itself down first (see sandbox), then runs the tutor until the
// reader leaves, the server says bye, or in ends.
func RunSession(in io.Reader, out io.Writer, ctlIn io.Reader, ctlOut io.Writer) error {
	ctl := newCtlWriter(ctlOut)
	msgs := make(chan Msg, 16)
	go func() {
		_ = readCtl(ctlIn, func(m Msg) { msgs <- m })
		close(msgs)
	}()
	init, ok := <-msgs
	if !ok || init.T != MsgInit {
		return errors.New("learnssh: no init message")
	}
	if err := sandbox(); err != nil {
		// Logged by the server from stderr. The session still runs: the
		// systemd unit's own sandbox is the outer wall.
		fmt.Fprintln(os.Stderr, "sandbox:", err)
	}

	file, err := lessons.Load()
	if err != nil {
		return err
	}

	// The browser build's setup, on a server: every pane is the fake shell on
	// an in-memory pty, and nothing reaches this machine's files.
	for k, v := range map[string]string{
		"TERM": "xterm-256color", "COLORTERM": "truecolor", "CLICOLOR_FORCE": "1",
		"HOME": webshell.Home, "USER": "guest", "SHELL": "/bin/sh",
	} {
		_ = os.Setenv(k, v)
	}
	webshell.SetFlavorSSH()
	ptyspawn.NewGuestPty = func(width, height int) (xpty.Pty, error) {
		return webshell.NewPty(width, height), nil
	}

	profile := colorprofile.ANSI256
	switch init.Color {
	case "truecolor":
		profile = colorprofile.TrueColor
	case "16":
		profile = colorprofile.ANSI
	}
	theme.SetColorProfile(profile)

	w, h := clampSize(init.W, init.H)
	cfg := learn.Config()
	app.SetInputHandler(input.HandleInput)
	config.ApplyAppearanceConfig(cfg, &config.Global)
	seed := config.AppearanceFrom(cfg, config.Overrides{})
	o := app.NewOS(app.OSOptions{
		Client:          app.ClientSSH,
		LearnMode:       true,
		GuestApps:       learn.LauncherApps(),
		ConfigReadOnly:  true,
		KeybindRegistry: config.NewKeybindRegistry(cfg),
		UserConfig:      cfg,
		Settings:        &seed,
		Width:           w,
		Height:          max(h-cardHeight, 10),
		Caps: &app.HostCapabilities{
			TrueColor:    profile == colorprofile.TrueColor,
			TerminalName: "tuios-learn",
		},
	})
	o.LearnNotes = sshNotes()

	pipe := newInputPipe()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := in.Read(buf)
			if n > 0 {
				pipe.write(buf[:n])
			}
			if err != nil {
				pipe.close()
				return
			}
		}
	}()

	var prog *tea.Program
	t := newTutor(file, init, ctl, pipe.write)
	t.send = func(msg tea.Msg) { go prog.Send(msg) }
	t.tour = learn.New(o, t.queue.push, t.send)
	t.w, t.h = w, h

	prog = tea.NewProgram(t, append(app.ProgramOptions(),
		tea.WithInput(pipe),
		tea.WithOutput(out),
		tea.WithWindowSize(w, h),
		tea.WithColorProfile(profile),
		tea.WithEnvironment([]string{"TERM=" + safeTerm(init.Term)}),
		tea.WithoutSignalHandler(),
		tea.WithFilter(filter),
	)...)
	t.queue.nudge = func() { go prog.Send(drainMsg{}) }

	go func() {
		for m := range msgs {
			if m.T == MsgResize {
				// As a size message, so the renderer resizes too.
				w, h := clampSize(m.W, m.H)
				prog.Send(tea.WindowSizeMsg{Width: w, Height: h})
				continue
			}
			prog.Send(ctlMsg(m))
		}
		// The server went away: nothing can reach the reader any more.
		prog.Send(byeMsg{text: ""})
	}()

	_, err = prog.Run()
	for _, win := range t.tour.OS.Windows {
		win.Close()
	}
	return err
}

// cardHeight is the rows the lesson card takes under tuios.
const cardHeight = 5

// Minimum and maximum terminal sizes. Below the minimum the tutor asks for a
// bigger window. Above the maximum the frame is drawn at the maximum, which
// bounds the memory a session's screens take.
const (
	MinCols, MinRows = 80, 24
	MaxCols, MaxRows = 320, 120
)

func clampSize(w, h int) (int, int) {
	return min(max(w, 20), MaxCols), min(max(h, 8), MaxRows)
}

// safeTerm is TERM for Bubble Tea's capability guesses: the client's value
// if it looks like a terminal name, else xterm-256color.
func safeTerm(term string) string {
	if term == "" || len(term) > 40 {
		return "xterm-256color"
	}
	for _, r := range term {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '.' || r == '_' || r == '+') {
			return "xterm-256color"
		}
	}
	return term
}

// sshNotes rewrites Learn mode's notes, which talk about a browser tab. The
// quit note shows nothing, because a quit opens the tutor's menu instead.
func sshNotes() map[string]string {
	out := map[string]string{}
	for _, note := range app.LearnUnavailableActions() {
		switch {
		case strings.Contains(note, "close the tab"):
			out[note] = ""
		case strings.HasPrefix(note, "Paste with your browser"):
			out[note] = "Paste with your terminal's own paste key."
		default:
			out[note] = strings.ReplaceAll(note, "browser demo", "SSH demo")
		}
	}
	return out
}

// filter is the program's event filter: the tour's quit guard and motion
// filter, except when the tutor itself is ending the session.
func filter(model tea.Model, msg tea.Msg) tea.Msg {
	t, ok := model.(*tutor)
	if !ok || t.quitting {
		return msg
	}
	if _, quit := msg.(tea.QuitMsg); quit {
		// A quit from inside tuios opens the menu instead.
		return openMenuMsg{}
	}
	msg = app.FilterLearnMode(t.tour.OS, msg)
	return app.FilterMouseMotion(t.tour.OS, msg)
}

// inputPipe is what Bubble Tea reads: the reader's bytes, and what "show me"
// types for them.
type inputPipe struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    []byte
	closed bool
}

func newInputPipe() *inputPipe {
	p := &inputPipe{}
	p.cond = sync.NewCond(&p.mu)
	return p
}

// maxPending bounds input waiting to be read. The server already limits the
// input rate; this is the second wall.
const maxPending = 64 << 10

func (p *inputPipe) write(b []byte) {
	p.mu.Lock()
	if len(p.buf)+len(b) <= maxPending {
		p.buf = append(p.buf, b...)
	}
	p.cond.Broadcast()
	p.mu.Unlock()
}

func (p *inputPipe) close() {
	p.mu.Lock()
	p.closed = true
	p.cond.Broadcast()
	p.mu.Unlock()
}

func (p *inputPipe) Read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for len(p.buf) == 0 && !p.closed {
		p.cond.Wait()
	}
	if len(p.buf) == 0 {
		return 0, io.EOF
	}
	n := copy(b, p.buf)
	p.buf = p.buf[n:]
	return n, nil
}
