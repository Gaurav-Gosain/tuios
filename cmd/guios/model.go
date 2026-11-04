package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The client model runs in this process. It is internal/app's OS, the model
// of the terminal client, as tuios gui-bridge runs it: a Bubble Tea program
// with no renderer and no input reader. Tiling, workspaces, focus, keybinds,
// the prefix key and every action behave as in the terminal client, because
// they are the terminal client's code.
//
// The window never reads the OS. After each message, the model goroutine
// copies what the window draws into a snapshot and swaps it in. Pane cells
// are the exception: the window reads each pane's emulator under the pane's
// own read lock, the lock the terminal client's compositor takes. There is no
// second emulator and no bridge process.

// paneSnap is one pane the window draws.
type paneSnap struct {
	ID, PTY, Title, Cwd       string
	Agent, AgentMsg, AgentKnd string
	Harness                   string
	X, Y, W, H, Border, Z     int
	Floating                  bool
	win                       *terminal.Window
}

// snapshot is everything the window draws, as of one model message.
type snapshot struct {
	Session    string
	Cols, Rows int
	Workspace  int
	NumWS      int
	WSNames    map[int]string
	Occupied   map[int]bool
	Focused    string
	Zoomed     string
	Tiling     bool
	Panes      []paneSnap
	Tree       []sessiontree.Node
	// Needs counts the agents waiting for the person, in every session.
	Needs int
}

// guiMsg runs a function on the model goroutine, where the OS may be read
// and changed.
type guiMsg func(m *model) tea.Cmd

// model wraps the OS for the program and publishes snapshots.
type model struct {
	os   *app.OS
	snap atomic.Pointer[snapshot]
	// notify asks the window for a frame. Set once the window exists.
	notify atomic.Pointer[func()]
	// clipboard writes the system clipboard. Set once the window exists.
	clipboard atomic.Pointer[func(string)]
	// tree is the session tree as last built, rebuilt on every message but
	// pane output.
	tree  []sessiontree.Node
	needs int
	// empty says the attached session had no panes.
	empty bool
}

func (m *model) Init() tea.Cmd {
	init := m.os.Init()
	// A new or empty session opens with a shell, as tuios attach does, and
	// tiles: guios lays panes out as cards.
	if m.empty && len(m.os.Windows) == 0 {
		cmds := []tea.Cmd{init}
		if !m.os.AutoTiling {
			if c, err := m.os.RunActionCmd("toggle_tiling"); err == nil {
				cmds = append(cmds, c)
			}
		}
		if open, err := m.os.RunActionCmd("new_window"); err == nil {
			cmds = append(cmds, open)
		}
		return tea.Batch(cmds...)
	}
	return init
}

func (m *model) View() tea.View  { return tea.NewView("") }
func (m *model) Snap() *snapshot { return m.snap.Load() }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if g, ok := msg.(guiMsg); ok {
		cmd = g(m)
	} else {
		next, c := m.os.Update(msg)
		if o, ok := next.(*app.OS); ok {
			m.os = o
		}
		cmd = c
	}
	cmd = m.intercept(cmd)
	_, output := msg.(app.PTYDataMsg)
	m.publish(!output)
	if output {
		// Pane cells changed. The snapshot may be the same.
		m.changed()
	}
	return m, cmd
}

// publish builds the snapshot and swaps it in when it differs from the last
// one. withTree rebuilds the session tree, which output alone never changes.
func (m *model) publish(withTree bool) {
	o := m.os
	if withTree || m.tree == nil {
		m.tree = o.BuildSessionTree().Sessions
		m.needs = 0
		for _, s := range m.tree {
			for _, w := range s.Children {
				if w.AgentState == "needs_input" {
					m.needs++
				}
			}
		}
	}
	s := &snapshot{
		Session:   o.SessionName,
		Cols:      o.GetRenderWidth(),
		Rows:      o.Height,
		Workspace: o.CurrentWorkspace,
		NumWS:     max(o.NumWorkspaces, 1),
		WSNames:   o.WorkspaceNames,
		Occupied:  map[int]bool{},
		Tiling:    o.AutoTiling,
		Tree:      m.tree,
		Needs:     m.needs,
	}
	if f := o.GetFocusedWindow(); f != nil {
		s.Focused = f.ID
	}
	for _, w := range o.Windows {
		if w == nil {
			continue
		}
		s.Occupied[w.Workspace] = true
		if w.Workspace != o.CurrentWorkspace || w.Minimized {
			continue
		}
		if w.Zoomed {
			s.Zoomed = w.ID
		}
		cwd := w.Cwd
		if cwd == "" {
			cwd = w.DaemonCwd
		}
		title := w.CustomName
		if title == "" {
			title = w.Title()
		}
		// A pane with no title of its own is named by what it runs, as the
		// rail names it.
		if w.CustomName == "" && w.ForegroundCmd != "" && (title == "" || strings.HasPrefix(title, "Terminal ")) {
			title = w.ForegroundCmd
		}
		s.Panes = append(s.Panes, paneSnap{
			ID: w.ID, PTY: w.PTYID, Title: title, Cwd: cwd,
			Agent: w.AgentState, AgentMsg: w.AgentMessage, AgentKnd: w.AgentKind, Harness: w.AgentHarness,
			X: w.X, Y: w.Y, W: w.Width, H: w.Height, Border: w.BorderOffset(), Z: w.Z,
			Floating: w.IsFloating || w.IsPopup, win: w,
		})
	}
	sort.SliceStable(s.Panes, func(i, j int) bool {
		a, b := s.Panes[i], s.Panes[j]
		if a.Floating != b.Floating {
			return !a.Floating
		}
		return a.Z < b.Z
	})
	if old := m.snap.Load(); old != nil && sameSnap(old, s) {
		return
	}
	m.snap.Store(s)
	m.changed()
}

// intercept takes the clipboard writes out of the model's commands. The
// terminal client writes the clipboard with OSC 52 to its terminal, which
// here is io.Discard: guios writes the system clipboard instead.
func (m *model) intercept(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg { return m.filter(cmd()) }
}

func (m *model) filter(msg tea.Msg) tea.Msg {
	switch v := msg.(type) {
	case nil:
		return nil
	case tea.BatchMsg:
		out := make(tea.BatchMsg, len(v))
		for i, c := range v {
			out[i] = m.intercept(c)
		}
		return out
	}
	if t := reflect.TypeOf(msg); t.PkgPath() == "charm.land/bubbletea/v2" && t.Name() == "setClipboardMsg" {
		if f := m.clipboard.Load(); f != nil {
			(*f)(reflect.ValueOf(msg).String())
		}
		return nil
	}
	return msg
}

func (m *model) changed() {
	if f := m.notify.Load(); f != nil {
		(*f)()
	}
}

// sameSnap compares two snapshots by what they show. Panes compare field by
// field, their windows by identity: a deep compare would walk a live
// emulator another goroutine writes.
func sameSnap(a, b *snapshot) bool {
	if len(a.Panes) != len(b.Panes) {
		return false
	}
	for i := range a.Panes {
		if a.Panes[i] != b.Panes[i] {
			return false
		}
	}
	x, y := *a, *b
	x.Panes, y.Panes = nil, nil
	return reflect.DeepEqual(x, y)
}

// session is the running model and its daemon connection.
type clientModel struct {
	m       *model
	program *tea.Program
	client  *session.TUIClient
}

// startModel connects to the daemon, attaches a session and runs the model.
// cols and rows are the stage in cells, cellW and cellH a cell in device
// pixels, insets the room around each pane's text (see stageInsets).
func startModel(version, name string, cols, rows, cellW, cellH int, insets terminal.PixelInsets) (*clientModel, error) {
	if insets.CellWidth > 0 {
		terminal.SetPixelInsets(&insets)
	}
	caps := &app.HostCapabilities{
		TrueColor:    true,
		TerminalName: "guios",
		CellWidth:    cellW,
		CellHeight:   cellH,
	}
	hello := app.ClientCapabilitiesOf(caps)
	client := session.NewTUIClient()
	client.Served = true
	client.SSHAuthSock = os.Getenv("SSH_AUTH_SOCK")
	if err := client.ConnectWithCapabilities(version, cols, rows, hello); err != nil {
		return nil, fmt.Errorf("could not reach the tuios daemon: %w", err)
	}
	state, err := client.AttachSession(name, true, cols, rows)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("could not attach session %q: %w", name, err)
	}
	if name == "" && state != nil {
		name = state.Name
	}
	client.StartReadLoop()

	o := newOS(app.OSOptions{
		Client:          app.ClientLocal,
		Width:           cols,
		Height:          rows,
		Caps:            caps,
		ConfigReadOnly:  true,
		IsDaemonSession: true,
		DaemonClient:    client,
		SessionName:     name,
	})
	o.SnapshotScrollback = guiScrollback
	o.WireDaemonClient(client)
	o.RestoreAttachedSession(state)
	// Keys go to the panes. The prefix key still reaches window management.
	o.Mode = app.TerminalMode
	reportResize(client)

	m := &model{os: o, empty: state == nil || len(state.Windows) == 0}
	popts := append([]tea.ProgramOption{
		tea.WithInput(nil),
		tea.WithOutput(io.Discard),
		tea.WithWindowSize(cols, rows),
		tea.WithoutRenderer(),
	}, app.ProgramOptions()...)
	p := tea.NewProgram(m, popts...)
	o.BindProgram(p)
	theme.SetColorProfile(colorprofile.TrueColor)
	m.publish(true)
	go func() {
		if _, err := p.Run(); err != nil {
			log.Printf("guios: model: %v", err)
		}
		o.Cleanup()
		_ = client.Close()
	}()
	return &clientModel{m: m, program: p, client: client}, nil
}

// newOS is the terminal client's model with the chrome guios draws itself
// turned off, as tuios gui-bridge sets it up: no dock, no rail, no borders
// between panes, no animations, and a zoom over the whole stage.
func newOS(opts app.OSOptions) *app.OS {
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		log.Printf("Warning: failed to load config, using defaults: %v", err)
		userConfig = config.DefaultConfig()
	}
	app.SetInputHandler(input.HandleInput)
	seed := config.AppearanceFrom(userConfig, chromeOverrides)
	seed.SidebarEnabled = false
	sessionBorders := config.AppearanceFrom(userConfig, config.Overrides{}).SharedBorders
	opts.KeybindRegistry = config.NewKeybindRegistry(userConfig)
	opts.UserConfig = userConfig
	opts.Settings = &seed
	o := app.NewOS(opts)
	o.ReloadFilter = withOwnChrome
	o.FullZoom = true
	o.NoRail = true
	o.OwnPaneBorders = true
	o.SessionSharedBorders = sessionBorders
	return o
}

var chromeOverrides = config.Overrides{
	DockbarPosition: "hidden",
	SharedBorders:   true,
	NoAnimations:    true,
	// The daemon keeps each pane's whole history. This client keeps a
	// short tail and asks the daemon for more when the person scrolls or
	// searches (M2). Kero keeps a full copy in its app and reached 215 MB
	// of Go heap against 17 MB in its daemon (kero #287, #288).
	ScrollbackLines: guiScrollback,
}

// guiScrollback is how many history rows each pane keeps in this process.
const guiScrollback = 500

func withOwnChrome(cfg *config.UserConfig) *config.UserConfig {
	c := *cfg
	on, off := true, false
	c.Appearance.DockbarPosition = chromeOverrides.DockbarPosition
	c.Appearance.SharedBorders = &on
	c.Appearance.Sidebar.Enabled = &off
	c.Appearance.ScrollbackLines = guiScrollback
	return &c
}

// do runs fn on the model goroutine. It does not wait.
func (c *clientModel) do(fn func(m *model) tea.Cmd) {
	go c.program.Send(guiMsg(fn))
}

// key sends a key to the model, which routes it as the terminal client
// routes a key: keybinds first, then the focused pane.
func (c *clientModel) key(k tea.KeyPressMsg) {
	c.program.Send(k)
}

// resize tells the model the stage's size in cells and the room around
// each pane's text.
func (c *clientModel) resize(cols, rows, cellW, cellH int, insets terminal.PixelInsets) {
	c.do(func(m *model) tea.Cmd {
		terminal.SetPixelInsets(&insets)
		for _, w := range m.os.Windows {
			if w != nil {
				w.CellPixelWidth, w.CellPixelHeight = cellW, cellH
			}
		}
		next, cmd := m.os.Update(tea.WindowSizeMsg{Width: cols, Height: rows})
		if n, ok := next.(*app.OS); ok {
			m.os = n
		}
		o := m.os
		for _, w := range o.Windows {
			if w != nil {
				w.Resize(w.Width, w.Height)
			}
		}
		reportResize(o.DaemonClient)
		return cmd
	})
}

// reportResize makes this client the one whose size the session takes,
// under window_size latest, as tuios gui-bridge does on attach and resize.
func reportResize(c *session.TUIClient) {
	if c == nil {
		return
	}
	if p := c.SessionWindowSize(); p == "" || p == config.WindowSizeLatest {
		c.ReportActivity(time.Now())
	}
}

// reportInput tells the daemon the person used the session.
func reportInput(c *session.TUIClient) {
	if c == nil {
		return
	}
	now := time.Now()
	c.ReportUsed(now)
	if p := c.SessionWindowSize(); p == "" || p == config.WindowSizeLatest {
		c.ReportActivity(now)
	}
}
