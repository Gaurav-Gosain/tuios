// Package guibridge runs a tuios client with no screen of its own and hands
// what it would draw to a separate renderer over a pipe: the layout of the
// session as JSON, and each pane's byte stream as binary frames. It is the
// daemon side of tuios-gpui, a native GPU client, and is reached through the
// hidden `tuios gui-bridge` command.
//
// The bridge runs the full client model (internal/app) without its renderer,
// so window placement, tiling, workspaces and routed commands behave exactly
// as in the terminal client. The renderer keeps its own emulator per pane and
// feeds it the stream the model's stream tap sees (see app.StreamTap).
//
// # Wire format
//
// Both directions carry frames: a 4-byte big-endian length (of everything
// after it), a 1-byte kind, then the payload.
//
// Bridge to renderer:
//
//	1 JSON    one JSON object, see Event. Besides the attached session's
//	          state, a "fleet" event carries every session and agent on the
//	          daemon whenever they change (see watchFleet).
//	2 OUTPUT  u8 id length, pane PTY id, then the bytes the pane produced
//	3 SNAP    u8 id length, PTY id, u16 cols, u16 rows, then VT bytes that
//	          rebuild the pane in a reset emulator of that size; with no
//	          bytes, the pane starts blank
//	4 RESIZE  u8 id length, PTY id, u16 cols, u16 rows; in band with OUTPUT
//
// Renderer to bridge:
//
//	1 JSON    one JSON object, see Command
//	2 INPUT   u8 id length, PTY id, then bytes for the pane's PTY
package guibridge

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/charmbracelet/colorprofile"
)

// Frame kinds.
const (
	KindJSON   = 1
	KindOutput = 2
	KindSnap   = 3
	KindResize = 4
	KindInput  = 2
)

// maxFrame bounds a frame read from the renderer.
const maxFrame = 16 << 20

// Options configures one bridge run.
type Options struct {
	// Session is the session to attach, created when missing. Empty picks
	// the first one the daemon lists, or "gui" when there is none.
	Session string
	// Cols and Rows are the renderer's grid; CellWidth and CellHeight its
	// cell size in pixels.
	Cols, Rows            int
	CellWidth, CellHeight int
	// Insets is the renderer's room around each pane's text: top, left,
	// right, bottom, in device pixels. See terminal.PixelInsets.
	Insets  []int
	Version string
	// Theme overrides the theme the user's config names. Empty keeps it.
	Theme string
	In    io.Reader
	Out   io.Writer
}

// Event is the JSON the bridge sends.
type Event struct {
	Type    string `json:"type"`
	Message string `json:"message,omitempty"`
	// State is set on "state" events.
	State *State `json:"state,omitempty"`
	// Theme is set on "theme" events.
	Theme *Theme `json:"theme,omitempty"`
	// Fleet is set on "fleet" events: every session and every pane's agent
	// state on the daemon, sent when they change.
	Fleet *Fleet `json:"fleet,omitempty"`
}

// State is the session as the renderer draws it. Positions are cells.
type State struct {
	Session        string         `json:"session"`
	Cols           int            `json:"cols"`
	Rows           int            `json:"rows"`
	Workspace      int            `json:"workspace"`
	NumWorkspaces  int            `json:"num_workspaces"`
	WorkspaceNames map[int]string `json:"workspace_names,omitempty"`
	// Occupied lists the workspaces that hold at least one window.
	Occupied []int    `json:"occupied"`
	Focused  string   `json:"focused"`
	Tiling   bool     `json:"tiling"`
	Windows  []Window `json:"windows"`
}

// Window is one pane.
type Window struct {
	ID        string `json:"id"`
	PTY       string `json:"pty"`
	Title     string `json:"title"`
	Name      string `json:"name,omitempty"`
	Workspace int    `json:"workspace"`
	X         int    `json:"x"`
	Y         int    `json:"y"`
	W         int    `json:"w"`
	H         int    `json:"h"`
	Z         int    `json:"z"`
	// Border is how many cells of each edge the window's frame takes; the
	// content is the rest.
	Border    int    `json:"border"`
	Minimized bool   `json:"minimized,omitempty"`
	Floating  bool   `json:"floating,omitempty"`
	Zoomed    bool   `json:"zoomed,omitempty"`
	Agent     string `json:"agent,omitempty"`
	AgentMsg  string `json:"agent_message,omitempty"`
	AgentKind string `json:"agent_kind,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
	// Foreground is what the pane runs, empty at a shell prompt.
	Foreground string `json:"foreground,omitempty"`
	// Harness is the agent harness that reported the state (claude, codex).
	Harness string `json:"harness,omitempty"`
	// AgentAt is when the pane entered its agent state, in Unix milliseconds.
	AgentAt int64 `json:"agent_at,omitempty"`
	// Repo and Branch describe the git checkout the pane's folder is in.
	Repo   string `json:"repo,omitempty"`
	Branch string `json:"branch,omitempty"`
}

// Command is the JSON the renderer sends.
type Command struct {
	Cmd string `json:"cmd"`
	// resize
	Cols       int `json:"cols,omitempty"`
	Rows       int `json:"rows,omitempty"`
	CellWidth  int `json:"cell_width,omitempty"`
	CellHeight int `json:"cell_height,omitempty"`
	// Insets is the room the renderer keeps around each pane's text, in
	// device pixels: top, left, right, bottom. See terminal.PixelInsets.
	// Absent keeps the panes at their full cell size.
	Insets []int `json:"insets,omitempty"`
	// focus
	Window string `json:"window,omitempty"`
	// workspace
	N int `json:"n,omitempty"`
	// theme: the theme to switch to; "" is the default colours.
	Theme string `json:"theme,omitempty"`
	// tape: any tape command by name, with its arguments
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
}

// Run attaches and serves the renderer until its input closes or the session
// ends.
func Run(opts Options) error {
	if opts.Cols <= 0 || opts.Rows <= 0 {
		opts.Cols, opts.Rows = 120, 40
	}
	// Before the first layout, so the panes start at the size they keep.
	setInsets(opts.Insets, opts.CellWidth, opts.CellHeight)
	out := newFrameWriter(opts.Out)
	defer out.Close()

	caps := &app.HostCapabilities{
		TrueColor:    true,
		TerminalName: "tuios-gpui",
		CellWidth:    opts.CellWidth,
		CellHeight:   opts.CellHeight,
	}
	client := session.NewTUIClient()
	// The size is the renderer's window, not this process's terminal, which
	// it has none of.
	client.Served = true
	if err := client.ConnectWithCapabilities(opts.Version, opts.Cols, opts.Rows, app.ClientCapabilitiesOf(caps)); err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	name := opts.Session
	if name == "" {
		if names := client.AvailableSessionNames(); len(names) > 0 {
			name = names[0]
		} else {
			name = "gui"
		}
	}
	state, err := client.AttachSession(name, true, opts.Cols, opts.Rows)
	if err != nil {
		_ = client.Close()
		return fmt.Errorf("failed to attach to session %q: %w", name, err)
	}
	client.StartReadLoop()

	osModel := newModel(app.OSOptions{
		Client:          app.ClientLocal,
		Width:           opts.Cols,
		Height:          opts.Rows,
		Caps:            caps,
		ConfigReadOnly:  true,
		IsDaemonSession: true,
		DaemonClient:    client,
		SessionName:     name,
	})
	osModel.SetStreamTap(&tap{out: out})
	osModel.WireDaemonClient(client)
	osModel.RestoreAttachedSession(state)

	m := &model{os: osModel, out: out, session: name}
	popts := append([]tea.ProgramOption{
		tea.WithInput(nil),
		tea.WithOutput(io.Discard),
		tea.WithWindowSize(opts.Cols, opts.Rows),
		tea.WithoutRenderer(),
	}, app.ProgramOptions()...)
	program := tea.NewProgram(m, popts...)
	osModel.BindProgram(program)

	// The renderer has a truecolor display whatever this process's stdout
	// says, and the theme it gets is worked out for that depth.
	theme.SetColorProfile(colorprofile.TrueColor)
	if opts.Theme != "" {
		_ = theme.Initialize(opts.Theme)
	}
	out.JSON(Event{Type: "attached", Message: name})
	th := CurrentTheme()
	out.JSON(Event{Type: "theme", Theme: &th})
	// Runs before out closes: deferred calls run last first.
	stopFleet := make(chan struct{})
	defer close(stopFleet)
	go watchFleet(stopFleet, out, opts.Version)

	go func() {
		err := readCommands(opts.In, client, program)
		if err != nil && !errors.Is(err, io.EOF) {
			log.Printf("gui-bridge: renderer input: %v", err)
		}
		program.Quit()
	}()

	_, err = program.Run()
	osModel.Cleanup()
	_ = client.Close()
	return err
}

// newModel is served.NewModel with the chrome a native renderer draws itself
// turned off, so the layout gives the panes the whole grid.
func newModel(opts app.OSOptions) *app.OS {
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		log.Printf("Warning: Failed to load config, using defaults: %v", err)
		userConfig = config.DefaultConfig()
	}
	app.SetInputHandler(input.HandleInput)
	seed := config.AppearanceFrom(userConfig, config.Overrides{
		DockbarPosition: "hidden",
		SharedBorders:   true,
		NoAnimations:    true,
	})
	seed.SidebarEnabled = false
	opts.KeybindRegistry = config.NewKeybindRegistry(userConfig)
	opts.UserConfig = userConfig
	opts.Settings = &seed
	return app.NewOS(opts)
}

// cmdMsg carries a renderer command into the Update loop.
type cmdMsg Command

// model wraps the client model: every message goes through it, and after each
// one the session's layout is sent to the renderer if it changed.
type model struct {
	os      *app.OS
	out     *frameWriter
	session string
	last    []byte
	gitSeen map[string]gitEntry
}

// gitEntry is a cached reading of a folder's checkout.
type gitEntry struct {
	repo, branch string
	at           time.Time
}

// git names the repository and branch of dir, read at most every few seconds
// per folder, since export runs after every message.
func (m *model) git(dir string) (string, string) {
	if dir == "" {
		return "", ""
	}
	if e, ok := m.gitSeen[dir]; ok && time.Since(e.at) < 4*time.Second {
		return e.repo, e.branch
	}
	st, ok := gitstate.Read(dir)
	e := gitEntry{at: time.Now()}
	if ok {
		e.repo, e.branch = st.Repo, st.Branch
	}
	if m.gitSeen == nil {
		m.gitSeen = map[string]gitEntry{}
	}
	m.gitSeen[dir] = e
	return e.repo, e.branch
}

func (m *model) Init() tea.Cmd { return m.os.Init() }

func (m *model) View() tea.View { return tea.NewView("") }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if c, ok := msg.(cmdMsg); ok {
		cmd = m.handle(Command(c))
	} else {
		next, c := m.os.Update(msg)
		if o, ok := next.(*app.OS); ok {
			m.os = o
		}
		cmd = c
	}
	m.export()
	return m, cmd
}

func (m *model) handle(c Command) tea.Cmd {
	switch c.Cmd {
	case "resize":
		if c.CellWidth > 0 && c.CellHeight > 0 {
			for _, w := range m.os.Windows {
				w.CellPixelWidth, w.CellPixelHeight = c.CellWidth, c.CellHeight
			}
		}
		changed := setInsets(c.Insets, c.CellWidth, c.CellHeight)
		next, cmd := m.os.Update(tea.WindowSizeMsg{Width: c.Cols, Height: c.Rows})
		if o, ok := next.(*app.OS); ok {
			m.os = o
		}
		if changed {
			// The layout may not move when only the insets change, so each
			// pane is told its new size here. Resize sends nothing for a
			// pane whose size stays the same.
			for _, w := range m.os.Windows {
				if w != nil {
					w.Resize(w.Width, w.Height)
				}
			}
		}
		return cmd
	case "focus":
		_ = m.os.FocusWindowByID(c.Window)
	case "workspace":
		_ = m.os.SwitchWorkspace(c.N)
	case "tape":
		// The routed-command path the daemon uses for `tuios run-command`, so
		// the renderer can run anything a tape can.
		if dropped := m.os.QueueRemoteCommand(&session.RemoteCommandPayload{
			CommandType: "tape_command",
			TapeCommand: c.Command,
			TapeArgs:    c.Args,
		}); dropped {
			log.Printf("gui-bridge: command queue full, dropped %s", c.Command)
		}
	case "theme":
		// The theme is this client's own setting, as in the terminal
		// client. Nothing is written to the config file.
		if err := theme.Initialize(c.Theme); err != nil {
			log.Printf("gui-bridge: theme %q: %v", c.Theme, err)
		}
		th := CurrentTheme()
		m.out.JSON(Event{Type: "theme", Theme: &th})
	case "quit":
		return tea.Quit
	}
	return nil
}

func (m *model) export() {
	o := m.os
	st := &State{
		Session:        m.session,
		Cols:           o.GetRenderWidth(),
		Rows:           o.Height,
		Workspace:      o.CurrentWorkspace,
		NumWorkspaces:  o.NumWorkspaces,
		WorkspaceNames: o.WorkspaceNames,
		Tiling:         o.AutoTiling,
		Windows:        make([]Window, 0, len(o.Windows)),
	}
	occupied := map[int]bool{}
	if f := o.GetFocusedWindow(); f != nil {
		st.Focused = f.ID
	}
	for _, w := range o.Windows {
		if w == nil {
			continue
		}
		occupied[w.Workspace] = true
		cwd := w.Cwd
		if cwd == "" {
			cwd = w.DaemonCwd
		}
		repo, branch := m.git(cwd)
		st.Windows = append(st.Windows, Window{
			ID: w.ID, PTY: w.PTYID, Title: w.Title(), Name: w.CustomName,
			Workspace: w.Workspace, X: w.X, Y: w.Y, W: w.Width, H: w.Height, Z: w.Z,
			Border: w.BorderOffset(), Minimized: w.Minimized, Floating: w.IsFloating,
			Zoomed: w.Zoomed, Agent: w.AgentState, AgentMsg: w.AgentMessage, AgentKind: w.AgentKind,
			Cwd: cwd, Foreground: w.ForegroundCmd, Harness: w.AgentHarness, AgentAt: w.AgentStateAt / int64(time.Millisecond),
			Repo: repo, Branch: branch,
		})
	}
	for ws := 1; ws <= max(o.NumWorkspaces, 9); ws++ {
		if occupied[ws] {
			st.Occupied = append(st.Occupied, ws)
		}
	}
	b, err := json.Marshal(Event{Type: "state", State: st})
	if err != nil || string(b) == string(m.last) {
		return
	}
	m.last = b
	m.out.Frame(KindJSON, b)
}

var insetsNow terminal.PixelInsets

// setInsets applies the renderer's insets and reports whether they changed.
// Anything other than four values turns them off.
func setInsets(v []int, cellW, cellH int) bool {
	var in terminal.PixelInsets
	if len(v) == 4 && cellW > 0 && cellH > 0 {
		in = terminal.PixelInsets{CellWidth: cellW, CellHeight: cellH, Top: v[0], Left: v[1], Right: v[2], Bottom: v[3]}
	}
	if in == insetsNow {
		return false
	}
	insetsNow = in
	if in.CellWidth == 0 {
		terminal.SetPixelInsets(nil)
	} else {
		terminal.SetPixelInsets(&in)
	}
	return true
}

// tap forwards the panes' streams to the renderer, in order.
type tap struct{ out *frameWriter }

func (t *tap) Snapshot(ptyID string, state *session.TerminalState) {
	var cols, rows int
	var vt []byte
	if state != nil {
		cols, rows = state.Width, state.Height
		vt = SnapshotVT(state)
	}
	b := make([]byte, 0, 1+len(ptyID)+4+len(vt))
	b = appendID(b, ptyID)
	b = binary.BigEndian.AppendUint16(b, uint16(cols))
	b = binary.BigEndian.AppendUint16(b, uint16(rows))
	b = append(b, vt...)
	t.out.Frame(KindSnap, b)
}

func (t *tap) Output(ptyID string, data []byte) {
	b := make([]byte, 0, 1+len(ptyID)+len(data))
	b = appendID(b, ptyID)
	b = append(b, data...)
	t.out.Frame(KindOutput, b)
}

func (t *tap) Resized(ptyID string, width, height int) {
	b := appendID(nil, ptyID)
	b = binary.BigEndian.AppendUint16(b, uint16(width))
	b = binary.BigEndian.AppendUint16(b, uint16(height))
	t.out.Frame(KindResize, b)
}

func appendID(b []byte, id string) []byte {
	if len(id) > 255 {
		id = id[:255]
	}
	b = append(b, byte(len(id)))
	return append(b, id...)
}

// readCommands reads renderer frames until EOF. Input goes straight to the
// daemon; everything else runs on the Update loop.
func readCommands(r io.Reader, client *session.TUIClient, p *tea.Program) error {
	br := bufio.NewReaderSize(r, 64<<10)
	var hdr [5]byte
	for {
		if _, err := io.ReadFull(br, hdr[:]); err != nil {
			return err
		}
		n := binary.BigEndian.Uint32(hdr[:4])
		if n < 1 || n > maxFrame {
			return fmt.Errorf("bad frame length %d", n)
		}
		body := make([]byte, n-1)
		if _, err := io.ReadFull(br, body); err != nil {
			return err
		}
		switch hdr[4] {
		case KindJSON:
			var c Command
			if err := json.Unmarshal(body, &c); err != nil {
				log.Printf("gui-bridge: bad command: %v", err)
				continue
			}
			p.Send(cmdMsg(c))
		case KindInput:
			if len(body) < 1 || len(body) < 1+int(body[0]) {
				continue
			}
			id := string(body[1 : 1+int(body[0])])
			if err := client.WritePTY(id, body[1+int(body[0]):]); err != nil {
				log.Printf("gui-bridge: input to %s: %v", id, err)
			}
		}
	}
}

// frameWriter serializes frames onto the renderer's pipe from any goroutine.
// Frames queue in memory, so a tap callback never blocks on the pipe.
type frameWriter struct {
	mu     sync.Mutex
	cond   *sync.Cond
	queue  [][]byte
	closed bool
	done   chan struct{}
}

func newFrameWriter(w io.Writer) *frameWriter {
	f := &frameWriter{done: make(chan struct{})}
	f.cond = sync.NewCond(&f.mu)
	go f.run(w)
	return f
}

func (f *frameWriter) Frame(kind byte, payload []byte) {
	b := make([]byte, 5, 5+len(payload))
	binary.BigEndian.PutUint32(b, uint32(1+len(payload)))
	b[4] = kind
	b = append(b, payload...)
	f.mu.Lock()
	if !f.closed {
		f.queue = append(f.queue, b)
		f.cond.Signal()
	}
	f.mu.Unlock()
}

func (f *frameWriter) JSON(v any) {
	b, err := json.Marshal(v)
	if err == nil {
		f.Frame(KindJSON, b)
	}
}

func (f *frameWriter) run(w io.Writer) {
	defer close(f.done)
	bw := bufio.NewWriterSize(w, 256<<10)
	for {
		f.mu.Lock()
		for len(f.queue) == 0 && !f.closed {
			f.cond.Wait()
		}
		q := f.queue
		f.queue = nil
		closed := f.closed
		f.mu.Unlock()
		for _, b := range q {
			if _, err := bw.Write(b); err != nil {
				return
			}
		}
		if err := bw.Flush(); err != nil || (closed && len(q) == 0) {
			return
		}
	}
}

func (f *frameWriter) Close() {
	f.mu.Lock()
	f.closed = true
	f.cond.Signal()
	f.mu.Unlock()
	<-f.done
}
