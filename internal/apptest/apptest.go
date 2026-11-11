// Package apptest drives a tuios client model the way a person and the
// daemon do, and reads back what a person would see.
//
// A test builds a Fixture with New. The session reaches the model as daemon
// state pushes, keys and mouse go through the real input router
// (input.HandleInput), and the screen is the frame the model hands Bubble Tea,
// written into a terminal emulator and read back cell by cell. Nothing here
// reaches into the model's unexported state, so a test written on it keeps
// passing while the model's internals are rearranged, and fails when what a
// person sees changes.
//
// Only tests import this package. The model, the theme and the input handler
// are process globals, so tests that use it do not run in parallel.
package apptest

import (
	"fmt"
	"maps"
	"os/exec"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"tuios.dev/vt"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Option sets up a Fixture.
type Option func(*setup)

type setup struct {
	w, h     int
	panes    map[int]int
	userConf []func(*config.UserConfig)
	settings []func(*config.Settings)
	profile  colorprofile.Profile
}

// Size is the host terminal's size. The default is 120 by 40.
func Size(w, h int) Option { return func(s *setup) { s.w, s.h = w, h } }

// Panes puts n panes on workspace 1.
func Panes(n int) Option { return func(s *setup) { s.panes[1] = n } }

// Workspaces puts panes on several workspaces, as workspace to count.
func Workspaces(counts map[int]int) Option {
	return func(s *setup) { maps.Copy(s.panes, counts) }
}

// Config edits the user config before the model is built, as a config file
// would.
func Config(fn func(*config.UserConfig)) Option {
	return func(s *setup) { s.userConf = append(s.userConf, fn) }
}

// Settings edits the resolved settings before the model is built.
func Settings(fn func(*config.Settings)) Option {
	return func(s *setup) { s.settings = append(s.settings, fn) }
}

// Colors16 draws the frame for a sixteen-colour terminal. There a list's
// cursor row is reverse video, which Highlighted reads, where at truecolor it
// is only a different ground.
func Colors16() Option { return func(s *setup) { s.profile = colorprofile.ANSI } }

// Fixture is one client model with the session it shows.
type Fixture struct {
	t testing.TB
	// M is the model. A test reads from it what the screen cannot show (the
	// layout mode, whether tiling is on) and never writes to it: a state the
	// test sets by hand is one no person or daemon can produce.
	M      *app.OS
	w, h   int
	screen *vt.Emulator
	pushes int
}

// New builds a client model the way an attached client starts: the model is
// made, the host reports its size, and the client attaches to the session,
// which arrives as the daemon's state. Later changes arrive as pushes (Push).
// There is no daemon connection, so a key that asks the daemon to open or
// close a pane has nobody to ask: a test of that needs a live daemon. The
// model starts the way a client does, in terminal mode on the focused pane.
func New(t testing.TB, opts ...Option) *Fixture {
	t.Helper()
	s := &setup{w: 120, h: 40, panes: map[int]int{}, profile: colorprofile.TrueColor}
	for _, o := range opts {
		o(s)
	}

	cfg := config.DefaultConfig()
	for _, fn := range s.userConf {
		fn(cfg)
	}
	settings := config.DefaultSettings()
	config.ApplyAppearanceConfig(cfg, &settings)
	// Motion off: a test reads the frame a change ends in, not one in the
	// middle of a slide. There is no clock to step the animations with.
	settings.Motion = config.MotionNone
	for _, fn := range s.settings {
		fn(&settings)
	}

	// A key that opens a pane with no daemon to ask spawns it here. It runs
	// true rather than a shell, so it starts nothing that lingers.
	if shell, err := exec.LookPath("true"); err == nil {
		t.Setenv("SHELL", shell)
	}

	// Some draw code reads the globals rather than the model's settings, so
	// the globals carry the fixture's too, and go back afterwards.
	prevGlobal := config.Global
	config.Global = settings
	prevProfile := theme.ColorProfile()
	theme.SetColorProfile(s.profile)
	app.SetInputHandler(input.HandleInput)

	m := app.NewOS(app.OSOptions{
		// A person at a terminal on this machine, as tuios itself starts.
		Client:          app.ClientLocal,
		UserConfig:      cfg,
		KeybindRegistry: config.NewKeybindRegistry(cfg),
		Settings:        &settings,
		Width:           s.w,
		Height:          s.h,
		SessionName:     "apptest",
		Caps:            &app.HostCapabilities{},
	})
	f := &Fixture{t: t, M: m, w: s.w, h: s.h}
	t.Cleanup(func() {
		for _, w := range m.Windows {
			w.Close()
		}
		m.Cleanup()
		app.SetInputHandler(nil)
		theme.SetColorProfile(prevProfile)
		config.Global = prevGlobal
	})

	f.attach(s)
	return f
}

// attach hands the model the session's state the way attaching does. The
// session tiles in BSP, whatever the config's startup layout says, so a test
// starts from the same layout everywhere; a test that wants another switches
// to it as a person does.
func (f *Fixture) attach(s *setup) {
	f.t.Helper()
	f.M.ApplyLayoutModeName(app.LayoutModeBSP)
	f.M.AutoTiling = true
	state := &session.SessionState{
		Name:             "apptest",
		CurrentWorkspace: 1,
		NumWorkspaces:    9,
		AutoTiling:       true,
		LayoutMode:       app.LayoutModeBSP,
		WorkspaceFocus:   map[int]string{},
		Version:          1,
	}
	n := 0
	for _, ws := range slices.Sorted(maps.Keys(s.panes)) {
		for range s.panes[ws] {
			n++
			id := fmt.Sprintf("pane-%032d", n)
			state.Windows = append(state.Windows, session.WindowState{
				ID:        id,
				PTYID:     "pty-" + id,
				Title:     fmt.Sprintf("pane %d", n),
				Width:     s.w,
				Height:    s.h,
				Workspace: ws,
				Unplaced:  true,
			})
			if ws == 1 {
				state.FocusedWindowID = id
			}
		}
	}
	f.pushes = 1
	f.M.RestoreAttachedSession(state)
	// The program reports the host's size once it starts, after the attach.
	f.Send(tea.WindowSizeMsg{Width: s.w, Height: s.h})
}

// push sends one daemon push. Each carries a newer version than the last, as
// the daemon's do.
func (f *Fixture) push(state *session.SessionState) {
	f.t.Helper()
	f.pushes++
	state.Version = f.pushes
	f.Send(app.StateSyncMsg{State: state})
}

// Push sends a daemon push: the session as the model holds it now, changed by
// edit.
func (f *Fixture) Push(edit func(*session.SessionState)) {
	f.t.Helper()
	state := f.M.BuildSessionState()
	edit(state)
	f.push(state)
}

// Send delivers one message to the model's Update, as the program loop does,
// then runs the commands it returned for as long as they answer at once.
func (f *Fixture) Send(msg tea.Msg) {
	f.t.Helper()
	_, cmd := f.M.Update(msg)
	f.run(cmd, 0)
	select {
	case <-f.M.PTYDataChan:
	default:
	}
}

// parked matches the commands the fixture does not run. A listener waits on
// one of the model's channels for as long as the model lives, and a timer
// sleeps in real time; the program loop parks both in a goroutine. There is
// no clock to step, so a test that needs a timer to fire says so and drives
// the effect another way.
var parked = regexp.MustCompile(strings.Join([]string{
	`internal/app\.(\(\*OS\)\.)?[Ll]isten`,     // channel listeners
	`internal/app\.\(\*OS\)\.tickUntilStop`,    // the maintenance ticker
	`charm\.land/bubbletea/v2\.(Tick|Every)\.`, // timers
}, "|"))

// cmdLimit is how long a command may run before the fixture calls it stuck.
// A command that is neither a listener nor a timer answers at once; one that
// blocks belongs on the parked list, and the failure names it.
const cmdLimit = 5 * time.Second

// maxCmdDepth bounds a chain of commands answering with more commands.
const maxCmdDepth = 16

func cmdName(cmd tea.Cmd) string {
	if fn := runtime.FuncForPC(reflect.ValueOf(cmd).Pointer()); fn != nil {
		return fn.Name()
	}
	return "unknown"
}

// run runs a command the way the program loop would, and hands what it
// answers back to Update.
func (f *Fixture) run(cmd tea.Cmd, depth int) {
	f.t.Helper()
	if cmd == nil {
		return
	}
	if depth > maxCmdDepth {
		f.t.Fatalf("apptest: commands answered with commands %d deep, last %s", depth, cmdName(cmd))
	}
	name := cmdName(cmd)
	if parked.MatchString(name) {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(cmdLimit):
		f.t.Fatalf("apptest: command %s has not answered after %v; if it waits by design, add it to parked", name, cmdLimit)
	}
	switch msg := msg.(type) {
	case nil, tea.QuitMsg:
	case tea.BatchMsg:
		for _, c := range msg {
			f.run(c, depth+1)
		}
	default:
		// tea.Sequence answers with an unexported slice of commands.
		if v := reflect.ValueOf(msg); v.Kind() == reflect.Slice && v.Type().Elem() == reflect.TypeFor[tea.Cmd]() {
			for i := range v.Len() {
				f.run(v.Index(i).Interface().(tea.Cmd), depth+1)
			}
			return
		}
		_, next := f.M.Update(msg)
		f.run(next, depth+1)
	}
}

// Keys presses keys by name, as Bubble Tea names them: "ctrl+b", "enter",
// "up", "G", "alt+left".
func (f *Fixture) Keys(names ...string) {
	f.t.Helper()
	for _, name := range names {
		f.Send(keyPress(name))
	}
}

// Type types text one character at a time.
func (f *Fixture) Type(text string) {
	f.t.Helper()
	for _, r := range text {
		f.Send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// Action runs a named action the way the command palette and remote commands
// run one. It fails the test when no action has the name.
func (f *Fixture) Action(name string) {
	f.t.Helper()
	handled, cmd := input.RunActionByName(name, f.M)
	if !handled {
		f.t.Fatalf("apptest: no action named %q", name)
	}
	f.run(cmd, 0)
}

// Wheel turns the wheel at a cell, down for a positive dy.
func (f *Fixture) Wheel(x, y, dy int) {
	f.t.Helper()
	button := tea.MouseWheelDown
	if dy < 0 {
		button, dy = tea.MouseWheelUp, -dy
	}
	for range dy {
		f.Send(tea.MouseWheelMsg(tea.Mouse{X: x, Y: y, Button: button}))
	}
}

// frame is the frame the model hands Bubble Tea, styled.
func (f *Fixture) frame() string {
	f.t.Helper()
	return f.M.View().Content
}

// draw writes the current frame into a terminal the host's size and returns
// it, so a test reads cells the way a terminal would show them.
func (f *Fixture) draw() *vt.Emulator {
	f.t.Helper()
	if f.screen == nil || f.screen.Width() != f.w || f.screen.Height() != f.h {
		f.screen = vt.NewEmulator(f.w, f.h)
	}
	var b strings.Builder
	b.WriteString("\x1b[0m\x1b[2J")
	for y, line := range strings.Split(f.frame(), "\n") {
		if y >= f.h {
			break
		}
		fmt.Fprintf(&b, "\x1b[%d;1H\x1b[0m%s", y+1, line)
	}
	if _, err := f.screen.Write([]byte(b.String())); err != nil {
		f.t.Fatalf("apptest: write frame: %v", err)
	}
	return f.screen
}

// Screen is the screen as text, one string per row, trailing blanks trimmed.
func (f *Fixture) Screen() []string {
	f.t.Helper()
	scr := f.draw()
	rows := make([]string, f.h)
	for y := range f.h {
		var b strings.Builder
		for x := 0; x < f.w; x++ {
			c := scr.CellAt(x, y)
			switch {
			case c == nil || c.Content == "":
				if c != nil && c.Width == 0 {
					continue // the second half of a wide glyph
				}
				b.WriteByte(' ')
			default:
				b.WriteString(c.Content)
			}
		}
		rows[y] = strings.TrimRight(b.String(), " ")
	}
	return rows
}

// Text is the screen as one string, rows joined by newlines.
func (f *Fixture) Text() string {
	f.t.Helper()
	return strings.Join(f.Screen(), "\n")
}

// Find is where text first appears on screen, reading rows top to bottom.
func (f *Fixture) Find(text string) (x, y int, ok bool) {
	f.t.Helper()
	for y, row := range f.Screen() {
		if before, _, found := strings.Cut(row, text); found {
			return len([]rune(before)), y, true
		}
	}
	return 0, 0, false
}

// Highlighted is the text of every row that carries a run of reverse-video
// cells at least minRun wide, trimmed, top to bottom. With Colors16 that is
// where a focused list draws its cursor.
func (f *Fixture) Highlighted() []string {
	f.t.Helper()
	const minRun = 3
	scr := f.draw()
	var out []string
	for y := range f.h {
		var run, best strings.Builder
		flush := func() {
			if len([]rune(run.String())) >= minRun && len(run.String()) > len(best.String()) {
				best.Reset()
				best.WriteString(run.String())
			}
			run.Reset()
		}
		for x := 0; x < f.w; x++ {
			c := scr.CellAt(x, y)
			if c == nil || c.Style.Attrs&uv.AttrReverse == 0 {
				flush()
				continue
			}
			if c.Content == "" {
				run.WriteByte(' ')
			} else {
				run.WriteString(c.Content)
			}
		}
		flush()
		if s := strings.TrimSpace(best.String()); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// keyPress builds the key press Bubble Tea delivers for a key name.
func keyPress(name string) tea.KeyPressMsg {
	msg := tea.KeyPressMsg{}
	for {
		prefix, rest, found := strings.Cut(name, "+")
		if !found || rest == "" {
			break
		}
		switch prefix {
		case "ctrl":
			msg.Mod |= tea.ModCtrl
		case "alt":
			msg.Mod |= tea.ModAlt
		case "shift":
			msg.Mod |= tea.ModShift
		default:
			return finishKey(msg, name)
		}
		name = rest
	}
	return finishKey(msg, name)
}

var namedKeys = map[string]rune{
	"esc": tea.KeyEscape, "enter": tea.KeyEnter, "tab": tea.KeyTab,
	"space": tea.KeySpace, "backspace": tea.KeyBackspace, "delete": tea.KeyDelete,
	"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
	"home": tea.KeyHome, "end": tea.KeyEnd, "pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown,
	"insert": tea.KeyInsert,
}

func finishKey(msg tea.KeyPressMsg, name string) tea.KeyPressMsg {
	if code, ok := namedKeys[name]; ok {
		msg.Code = code
		if code == tea.KeySpace && msg.Mod == 0 {
			msg.Text = " "
		}
		return msg
	}
	r := []rune(name)
	if len(r) == 0 {
		return msg
	}
	msg.Code = r[0]
	if r[0] >= 'A' && r[0] <= 'Z' && msg.Mod == 0 {
		// A capital is a shifted letter: Bubble Tea names it by its text.
		msg.Code, msg.ShiftedCode, msg.Mod = r[0]+('a'-'A'), r[0], tea.ModShift
	}
	if msg.Mod&^tea.ModShift == 0 {
		msg.Text = string(r[0])
	}
	return msg
}

// RightClick is a right button press at a cell. Send it, then a release, or
// use it alone where a press is what opens a menu.
func RightClick(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseRight})
}

// RightRelease is the release that ends a RightClick.
func RightRelease(x, y int) tea.MouseReleaseMsg {
	return tea.MouseReleaseMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseRight})
}

// Box is a frame on screen: its corners' columns and rows, and whether it
// is a pane's (its top edge carries window buttons) or a panel's.
type Box struct {
	X0, Y0, X1, Y1 int
	Pane           bool
	// Title is the text on the top edge, without the line and the window
	// buttons.
	Title string
}

// frameKind is one family of frame glyphs: rounded, square or heavy. The
// double family is left out: tuios draws its logo in it.
type frameKind struct{ tl, tr, bl, br, h, v string }

var frameKinds = []frameKind{
	{"╭", "╮", "╰", "╯", "─", "│"},
	{"┌", "┐", "└", "┘", "─", "│"},
	{"┏", "┓", "┗", "┛", "━", "┃"},
}

// Panels finds the panel frames on screen: every box whose top edge starts
// with a line rather than window buttons. A frame that is partly covered by
// another is not counted. One that does not close on screen, because the
// panel is bigger than the screen or a row of it is padded wrong, is
// returned in cut with where it starts and why.
func (f *Fixture) Panels() (whole []Box, cut []string) {
	f.t.Helper()
	return f.frames(false)
}

// PaneFrames finds the panes that draw their own frame, the same way:
// whole frames, and the ones the screen cuts.
func (f *Fixture) PaneFrames() (whole []Box, cut []string) {
	f.t.Helper()
	return f.frames(true)
}

func (f *Fixture) frames(panes bool) (whole []Box, cut []string) {
	grid := f.grid()
	at := func(x, y int) string {
		if y < 0 || y >= len(grid) || x < 0 || x >= len(grid[y]) {
			return ""
		}
		return grid[y][x]
	}
	row := func(y int) string { return strings.Join(grid[y], "") }
	for y0 := range f.h {
		for x0 := range f.w {
			var k frameKind
			found := false
			for _, fk := range frameKinds {
				if at(x0, y0) != fk.tl {
					continue
				}
				// A pane's top edge opens with its window buttons, a
				// panel's with a line.
				buttons := at(x0+1, y0) == " " && (at(x0+2, y0) == "●" || at(x0+2, y0) == "○")
				if (panes && buttons) || (!panes && at(x0+1, y0) == fk.h) {
					k, found = fk, true
				}
			}
			if !found {
				continue
			}
			x1, covered := x0+1, false
			for ; x1 < f.w; x1++ {
				c := at(x1, y0)
				if c == k.tr {
					break
				}
				if c == k.v || c == k.tl || c == k.bl || c == k.br {
					covered = true
					break
				}
			}
			if covered {
				continue
			}
			if x1 >= f.w {
				cut = append(cut, fmt.Sprintf("the frame at (%d,%d) runs off the right of the screen: %q", x0, y0, row(y0)))
				continue
			}
			y1 := y0 + 1
			for y1 < f.h && at(x0, y1) == k.v {
				y1++
			}
			if y1 >= f.h {
				cut = append(cut, fmt.Sprintf("the frame at (%d,%d) runs off the bottom of the screen", x0, y0))
				continue
			}
			if at(x0, y1) != k.bl {
				continue // covered on its left
			}
			// The right edge: another frame's glyph there means this one is
			// partly covered; anything else means the row does not end where
			// the frame does.
			bad, hidden := "", false
			for r := y0 + 1; r <= y1 && bad == "" && !hidden; r++ {
				want := k.v
				if r == y1 {
					want = k.br
				}
				switch c := at(x1, r); {
				case c == want:
				case isFrameGlyph(c):
					hidden = true
				default:
					bad = fmt.Sprintf("the frame at (%d,%d) has no right edge at column %d on row %d: %q", x0, y0, x1, r, row(r))
				}
			}
			if hidden {
				continue
			}
			if bad != "" {
				cut = append(cut, bad)
				continue
			}
			title := strings.TrimFunc(strings.Join(grid[y0][x0+1:x1], ""), func(r rune) bool {
				// The pill title style caps the text with private-use glyphs.
				return unicode.IsSpace(r) || unicode.Is(unicode.Co, r) || string(r) == k.h || r == '●' || r == '○'
			})
			whole = append(whole, Box{X0: x0, Y0: y0, X1: x1, Y1: y1, Pane: panes, Title: title})
		}
	}
	return whole, cut
}

// isFrameGlyph reports whether c is part of any frame: an edge or a corner.
func isFrameGlyph(c string) bool {
	for _, k := range frameKinds {
		if c == k.tl || c == k.tr || c == k.bl || c == k.br || c == k.h || c == k.v {
			return true
		}
	}
	return c == "┼" || c == "┴" || c == "┬" || c == "├" || c == "┤"
}

// grid is the screen as one string per cell.
func (f *Fixture) grid() [][]string {
	scr := f.draw()
	out := make([][]string, f.h)
	for y := range f.h {
		row := make([]string, f.w)
		for x := range f.w {
			if c := scr.CellAt(x, y); c != nil {
				row[x] = c.Content
			}
		}
		out[y] = row
	}
	return out
}

// PaneCenter is a cell in the middle of the focused pane's content, for a
// click that lands on the pane and not its frame or the chrome. It fails the
// test when no pane is focused.
func (f *Fixture) PaneCenter() (x, y int) {
	f.t.Helper()
	w := f.M.GetFocusedWindow()
	if w == nil {
		f.t.Fatal("apptest: no focused pane to click in")
	}
	return w.X + w.Width/2, w.Y + w.Height/2
}
