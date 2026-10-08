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
	"os"
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
	// SessionID attaches the session with this id when the daemon holds it,
	// before Session is looked at. With neither, the bridge attaches the
	// session last active. Session ids survive a rename and a restore.
	SessionID string
	// Host attaches a session on that host's daemon, through this machine's
	// daemon and its link. Empty is this machine.
	Host string
	// Resume is the stream position the renderer's emulator holds for each
	// pane, from an earlier bridge. A pane listed here is caught up from that
	// position instead of being sent a snapshot. See resume.go.
	Resume map[string]int64
	// ResumePID is the daemon the positions in Resume came from. A daemon
	// with another pid has new streams, and Resume is not used.
	ResumePID int
	// Scrollback is how many history rows each pane's snapshot carries: the
	// renderer's own history. 0 is the daemon's default.
	Scrollback int
	In         io.Reader
	Out        io.Writer
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
	// Keybinds is set on "keybinds" events: the user's keys, sent after
	// "attached" and again whenever the config reloads.
	Keybinds *Keybinds `json:"keybinds,omitempty"`
	// Result is set on "result" events, the answer to an action or layout
	// command. It follows the state that shows the command's effect.
	Result *Result `json:"result,omitempty"`
	// VerbResult is set on "verb_result" events, the answer to a verb
	// command. See verb.go.
	VerbResult *VerbResult `json:"verb_result,omitempty"`
	// Attention is set on "attention" events: every open Inbox item, sent
	// when the list changes. Notify is set on "notify" events, one per alert.
	// Detached is set on the "detached" event, sent once before the bridge
	// exits when the daemon ended the attach. See inbox.go.
	Attention *Attention `json:"attention,omitempty"`
	Notify    *Notify    `json:"notify,omitempty"`
	Detached  *Detached  `json:"detached,omitempty"`
	// Palette is set on "palette" events: tuios's command palette rows, sent
	// after "keybinds" and again when they change. See palette.go.
	Palette []PaletteRow `json:"palette,omitempty"`
	// Options is set on "options" events, the answer to an options command:
	// every option with the value the config file gives it. Req is the
	// command's req.
	Options *OptionList `json:"options,omitempty"`
	// Launcher is set on "launcher" events, the answer to a launcher
	// command: every program the launcher offers. See launcher.go.
	Launcher []LauncherEntry `json:"launcher,omitempty"`
	// Layouts is set on "layouts" events, the answer to a layouts command:
	// the saved layout templates. See layouts.go.
	Layouts []SavedLayout `json:"layouts,omitempty"`
	Req     int64         `json:"req,omitempty"`
	// Attached is set on the "attached" event: which session, on which
	// daemon. See resume.go.
	Attached *Attached `json:"attached,omitempty"`
	// PTY, Seq and Kept are set on "seq" events, sent before a pane's
	// snapshot, or in its place when the renderer keeps its emulator. See
	// resume.go.
	PTY  string `json:"pty,omitempty"`
	Seq  int64  `json:"seq,omitempty"`
	Kept bool   `json:"kept,omitempty"`
	// Hosts is set on "hosts" events: every machine in the [hosts] table,
	// with its link, its round trip and its sessions. See hosts.go.
	Hosts *Hosts `json:"hosts,omitempty"`
}

// Result answers one action or layout command.
type Result struct {
	// Req is the command's req, so the renderer can match the answer.
	Req int64 `json:"req,omitempty"`
	// Cmd is the command answered: "action" or "layout". Op is the layout
	// op, and Name the action.
	Cmd  string `json:"cmd"`
	Op   string `json:"op,omitempty"`
	Name string `json:"name,omitempty"`
	OK   bool   `json:"ok"`
	// Error says why the command did nothing, when OK is false.
	Error string `json:"error,omitempty"`
	// Ratio is the ratio a set-ratio left the split at, after the smallest
	// pane size held it.
	Ratio float64 `json:"ratio,omitempty"`
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
	// Layout is the layout on screen: "floating" (tiling off), "bsp",
	// "master_stack" or "scrolling". Trees are sent only for "bsp".
	Layout string `json:"layout"`
	// Trees is each workspace's split tree, keyed by workspace number.
	Trees map[string]*Tree `json:"trees,omitempty"`
	// Zoomed is the window ID of the zoomed pane, if one is.
	Zoomed string `json:"zoomed,omitempty"`
	// SessionSize is the size the daemon gives the session, which every
	// client shares (the smallest client's by default). ClientSize is this
	// bridge's own size, the renderer's grid. Both in cells.
	SessionSize Size `json:"session_size"`
	ClientSize  Size `json:"client_size"`
	// Viewport is set while the session is larger than this client: the
	// client shows the part of it that starts at X, Y (session cells).
	Viewport *Point `json:"viewport,omitempty"`
	// AgentSeen is set once an agent has been seen in the session. Until
	// then the renderer shows no agent chrome.
	AgentSeen bool `json:"agent_seen"`
	// Multifocus lists the panes that take typing together (the group),
	// and Broadcast says typing goes to all of them now. Omitted when no
	// pane is in the group.
	Multifocus []string `json:"multifocus,omitempty"`
	// PiP is the pane pinned as this client's picture in picture.
	PiP *PiP `json:"pip,omitempty"`
	// ScratchBox is the box the scratch panes show in, frame included, while
	// a scratch group is on screen.
	ScratchBox *Rect `json:"scratch_box,omitempty"`
	// ScratchOver is the workspace the scratch box shows over.
	ScratchOver int `json:"scratch_over,omitempty"`
	// Strip is the scrolling layout's strip, while layout is "scrolling".
	Strip *Strip `json:"strip,omitempty"`
}

// PiP is the pinned pane and the corner it goes in: bottom-right,
// bottom-left, top-right or top-left.
type PiP struct {
	Window string `json:"window"`
	Corner string `json:"corner"`
}

// Strip is the scrolling layout of the workspace on screen as it settles.
// Positions are strip cells: a pane's X counts from the strip's left end.
// The screen shows the strip from Viewport on.
type Strip struct {
	Viewport int         `json:"viewport"`
	Width    int         `json:"width"`
	Panes    []StripPane `json:"panes"`
}

// StripPane is one pane's place on the strip.
type StripPane struct {
	ID string `json:"id"`
	X  int    `json:"x"`
	Y  int    `json:"y"`
	W  int    `json:"w"`
	H  int    `json:"h"`
}

// Size is a size in cells.
type Size struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// Point is a position in cells.
type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// Window is one pane.
type Window struct {
	ID  string `json:"id"`
	PTY string `json:"pty"`
	// Kind is what draws the pane: "terminal", or "view", "web" or "app"
	// for a pane the renderer draws natively. URI names what it shows.
	Kind      string `json:"kind"`
	URI       string `json:"uri,omitempty"`
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
	Border    int  `json:"border"`
	Minimized bool `json:"minimized,omitempty"`
	Floating  bool `json:"floating,omitempty"`
	Zoomed    bool `json:"zoomed,omitempty"`
	// Popup marks a pane `tuios popup` opened: floating, closed by its
	// program. Scratch marks a pane of a scratch group.
	Popup   bool `json:"popup,omitempty"`
	Scratch bool `json:"scratch,omitempty"`
	// Stack names the stack the pane is in, and StackIndex its place there.
	// The stack's open member is the one that is not minimized; the others
	// show as title rows, above or below it in order.
	Stack      string `json:"stack,omitempty"`
	StackIndex int    `json:"stack_index,omitempty"`
	Agent      string `json:"agent,omitempty"`
	AgentMsg   string `json:"agent_message,omitempty"`
	AgentKind  string `json:"agent_kind,omitempty"`
	Cwd        string `json:"cwd,omitempty"`
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
	// Req is echoed in the result of an action or layout command.
	Req int64 `json:"req,omitempty"`
	// action: a keybinding action by its registry name.
	Name string `json:"name,omitempty"`
	// layout: the op and its arguments. See layout.go.
	Op     string  `json:"op,omitempty"`
	Split  uint64  `json:"split,omitempty"`
	Ratio  float64 `json:"ratio,omitempty"`
	A      string  `json:"a,omitempty"`
	B      string  `json:"b,omitempty"`
	ID     string  `json:"id,omitempty"`
	Target string  `json:"target,omitempty"`
	Side   string  `json:"side,omitempty"`
	Rect   *Rect   `json:"rect,omitempty"`
	// verb: a daemon verb by name, and its params. The answer is a
	// "verb_result" event with the same req. See verb.go.
	Verb   string         `json:"verb,omitempty"`
	Params map[string]any `json:"params,omitempty"`
	// switch-session: Create makes the session when it is missing, and
	// Restore restores it from its saved state first.
	Create  bool `json:"create,omitempty"`
	Restore bool `json:"restore,omitempty"`
	// theme: Persist writes the theme to the user's config file.
	Persist bool `json:"persist,omitempty"`
	// option: Key is an option path from list-options, Value its new value.
	Key   string `json:"key,omitempty"`
	Value string `json:"value,omitempty"`
	// group: On puts the pane ID in the group, off takes it out.
	On bool `json:"on,omitempty"`
	// launch: Path names the program, and Type types its command line at
	// a new pane's prompt instead of running it.
	Path string `json:"path,omitempty"`
	Type bool   `json:"type,omitempty"`
	// switch-session: Resume is the stream position of each pane the
	// renderer kept from the last time it showed the session. See resume.go.
	Resume map[string]int64 `json:"resume,omitempty"`
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
	// The renderer runs where the person is, so its agent socket is theirs.
	client.SSHAuthSock = os.Getenv("SSH_AUTH_SOCK")
	if opts.Host == "" {
		if err := client.ConnectWithCapabilities(opts.Version, opts.Cols, opts.Rows, app.ClientCapabilitiesOf(caps)); err != nil {
			return fmt.Errorf("failed to connect to daemon: %w", err)
		}
	} else if _, err := client.ConnectThroughHost(opts.Host, opts.Version, opts.Cols, opts.Rows, app.ClientCapabilitiesOf(caps)); err != nil {
		return fmt.Errorf("failed to connect to %s: %w", opts.Host, err)
	}
	cached := client.CachedSessions()
	if opts.SessionID != "" {
		// The handshake's listing can come after this point; ask.
		if listed := listSessions(opts.Version, opts.Host); len(listed) > 0 {
			cached = listed
		}
	}
	name := pickSession(cached, opts.SessionID, opts.Session)
	state, err := client.AttachSession(name, true, opts.Cols, opts.Rows)
	if err != nil {
		_ = client.Close()
		return fmt.Errorf("failed to attach to session %q: %w", name, err)
	}
	if name == "" && state != nil {
		name = state.Name
	}
	// From the listing made before the attach, which clears the restored
	// mark.
	info := attachedInfo(cached, name, opts.Host)
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
		AttachedHost:    opts.Host,
	})
	streams := &tap{out: out}
	if len(opts.Resume) > 0 && (opts.Host != "" || opts.ResumePID == info.DaemonPID) {
		streams.resume = opts.Resume
	}
	osModel.SetStreamTap(streams)
	osModel.SnapshotScrollback = max(opts.Scrollback, 0)
	osModel.WireDaemonClient(client)
	osModel.RestoreAttachedSession(state)

	m := &model{os: osModel, out: out, version: opts.Version, nonce: client.HumanNonce, host: opts.Host, tap: streams}
	popts := append([]tea.ProgramOption{
		tea.WithInput(nil),
		tea.WithOutput(io.Discard),
		tea.WithWindowSize(opts.Cols, opts.Rows),
		tea.WithoutRenderer(),
	}, app.ProgramOptions()...)
	program := tea.NewProgram(m, popts...)
	m.program = program
	osModel.BindProgram(program)

	// The renderer has a truecolor display whatever this process's stdout
	// says, and the theme it gets is worked out for that depth.
	theme.SetColorProfile(colorprofile.TrueColor)
	if opts.Theme != "" {
		_ = theme.Initialize(opts.Theme)
	}
	if info.SessionID == "" {
		info.SessionID = m.sessionID(name)
	}
	out.JSON(Event{Type: "attached", Message: name, Attached: &info})
	th := CurrentTheme()
	out.JSON(Event{Type: "theme", Theme: &th})
	m.sendKeybinds()
	// Runs before out closes: deferred calls run last first.
	stopFleet := make(chan struct{})
	defer close(stopFleet)
	go watchFleet(stopFleet, out, opts.Version)
	go watchHosts(stopFleet, out, opts.Version)

	go func() {
		err := readCommands(opts.In, client, program)
		if err != nil && !errors.Is(err, io.EOF) {
			log.Printf("gui-bridge: renderer input: %v", err)
		}
		// The renderer is gone. Leave the session as a detach does, so the
		// daemon counts this client out at once and the session size follows
		// the clients that are still there.
		if err := client.Detach(); err != nil {
			log.Printf("gui-bridge: detach: %v", err)
		}
		program.Quit()
	}()

	_, err = program.Run()
	// The daemon ended the attach: say why, before the pipe closes.
	if d := detachedEvent(m.os); d != nil {
		out.JSON(Event{Type: "detached", Detached: d})
	}
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
	seed := config.AppearanceFrom(userConfig, chromeOverrides)
	seed.SidebarEnabled = false
	opts.KeybindRegistry = config.NewKeybindRegistry(userConfig)
	opts.UserConfig = userConfig
	opts.Settings = &seed
	o := app.NewOS(opts)
	o.ReloadFilter = withOwnChrome
	// The renderer draws a zoom itself, over the other panes, and they keep
	// their sizes. A zoom of part of the screen would resize them for every
	// client of the session.
	o.FullZoom = true
	return o
}

// chromeOverrides turn off the chrome the renderer draws itself: the dock,
// the borders between panes and the animations. The sidebar goes too.
var chromeOverrides = config.Overrides{
	DockbarPosition: "hidden",
	SharedBorders:   true,
	NoAnimations:    true,
}

// withOwnChrome is a reloaded config with the chrome the renderer draws
// itself turned off. A reload applies the file's appearance to the model
// whole, and lays the panes out and tells the daemon before anything else
// runs: the dock came back above the panes, the session's panes moved down
// two rows for every client, and the renderer drew them short. A change to
// any option, or a host added, reloads the file. Only the copy changes.
func withOwnChrome(cfg *config.UserConfig) *config.UserConfig {
	c := *cfg
	on, off := true, false
	c.Appearance.DockbarPosition = chromeOverrides.DockbarPosition
	c.Appearance.SharedBorders = &on
	c.Appearance.Sidebar.Enabled = &off
	return &c
}

// cmdMsg carries a renderer command into the Update loop.
type cmdMsg Command

// model wraps the client model: every message goes through it, and after each
// one the session's layout is sent to the renderer if it changed.
type model struct {
	os      *app.OS
	out     *frameWriter
	last    []byte
	gitSeen map[string]gitEntry
	// gitBusy marks the folders a read is out for, and program is where the
	// read's answer goes. See git.
	gitBusy map[string]bool
	program *tea.Program
	// keysFrom is the config the last keybinds event was built from. A
	// reload replaces the registry's config, and the next message sends the
	// keys again.
	keysFrom *config.UserConfig
	// results wait for the state that shows their effect.
	results []Result
	// version is this build's, for the verb connections. nonce reads the
	// attach nonce the daemon issued to this client, for the person verbs.
	version string
	nonce   func() string
	// attentionSent, attentionGen and attentionLive are the Inbox as last
	// sent, and notified the alerts already sent. See inbox.go.
	attentionSent bool
	attentionGen  uint64
	attentionLive bool
	notified      map[string]bool
	// paletteSent is the palette as last sent, and paletteKey what it was
	// built from. See palette.go.
	paletteSent string
	paletteKey  string
	// launcherWanted is set while a launcher command waits for its scan.
	launcherWanted bool
	// host is the machine the session is on, empty for this one, and tap
	// the stream tap, which a switch-session hands its resume positions.
	host string
	tap  *tap
}

// sendKeybinds sends the keybinds event and notes the config it came from.
func (m *model) sendKeybinds() {
	if r := m.os.KeybindRegistry; r != nil {
		m.keysFrom = r.GetConfig()
	}
	m.out.JSON(Event{Type: "keybinds", Keybinds: exportKeybinds(m.os)})
	m.paletteKey = ""
}

// gitEntry is a cached reading of a folder's checkout.
type gitEntry struct {
	repo, branch string
	at           time.Time
}

// gitReadMsg carries a finished read of a folder's checkout into Update.
type gitReadMsg struct {
	dir   string
	entry gitEntry
}

// git names the repository and branch of dir from the last reading, and
// starts a new reading when that one is older than a few seconds. The read
// runs on its own goroutine, because it can run git, and export runs after
// every message on the model's goroutine. Until the first reading lands the
// folder has no repository, and the state that follows the reading has it.
func (m *model) git(dir string) (string, string) {
	if dir == "" {
		return "", ""
	}
	e, ok := m.gitSeen[dir]
	if (!ok || time.Since(e.at) >= 4*time.Second) && !m.gitBusy[dir] && m.program != nil {
		if m.gitBusy == nil {
			m.gitBusy = map[string]bool{}
		}
		m.gitBusy[dir] = true
		p := m.program
		go func() {
			st, ok := gitstate.Read(dir)
			e := gitEntry{at: time.Now()}
			if ok {
				e.repo, e.branch = st.Repo, st.Branch
			}
			p.Send(gitReadMsg{dir: dir, entry: e})
		}()
	}
	return e.repo, e.branch
}

func (m *model) Init() tea.Cmd { return m.os.Init() }

func (m *model) View() tea.View { return tea.NewView("") }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if g, ok := msg.(gitReadMsg); ok {
		if m.gitSeen == nil {
			m.gitSeen = map[string]gitEntry{}
		}
		m.gitSeen[g.dir] = g.entry
		delete(m.gitBusy, g.dir)
	} else if c, ok := msg.(cmdMsg); ok {
		cmd = m.handle(Command(c))
	} else {
		next, c := m.os.Update(msg)
		if o, ok := next.(*app.OS); ok {
			m.os = o
		}
		cmd = c
		if m.launcherWanted && isPathApps(msg) {
			m.sendLauncher()
		}
	}
	if r := m.os.KeybindRegistry; r != nil && r.GetConfig() != m.keysFrom {
		m.sendKeybinds()
	}
	if key := fmt.Sprint(m.keysFrom != nil, m.os.AgentsSeen(), len(m.os.MultifocusSet) > 0, m.keysFrom); key != m.paletteKey {
		m.paletteKey = key
		m.sendPalette()
	}
	m.export()
	m.exportAttention()
	m.exportNotify()
	for i := range m.results {
		m.out.JSON(Event{Type: "result", Result: &m.results[i]})
	}
	m.results = m.results[:0]
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
		_ = m.os.FocusPane(c.Window)
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
		// client. With persist it is also written to the config file, as the
		// terminal client's theme picker writes it.
		res := Result{Req: c.Req, Cmd: c.Cmd, Name: c.Theme}
		err := theme.Initialize(c.Theme)
		if err != nil {
			log.Printf("gui-bridge: theme %q: %v", c.Theme, err)
		} else if c.Persist {
			err = writeOption("appearance.theme", c.Theme)
		}
		th := CurrentTheme()
		m.out.JSON(Event{Type: "theme", Theme: &th})
		if c.Req != 0 {
			m.answer(res, err)
		}
	case "switch-session":
		res := Result{Req: c.Req, Cmd: c.Cmd, Name: c.Name}
		m.answer(res, m.switchSession(c))
	case "palette":
		res := Result{Req: c.Req, Cmd: c.Cmd, Name: c.Name}
		cmd, err := m.runPalette(c.Name)
		m.answer(res, err)
		return cmd
	case "option":
		res := Result{Req: c.Req, Cmd: c.Cmd, Name: c.Key}
		m.answer(res, writeOption(c.Key, c.Value))
	case "options":
		sendOptions(m.out, c.Req)
	case "launcher":
		return m.askLauncher()
	case "layouts":
		sendLayouts(m.out, c.Req)
	case "layout-save", "layout-load", "layout-delete":
		res := Result{Req: c.Req, Cmd: c.Cmd, Name: c.Name}
		m.answer(res, m.layoutTemplate(c.Cmd, c.Name))
	case "launch":
		res := Result{Req: c.Req, Cmd: c.Cmd, Name: c.Path}
		cmd, err := m.launch(c.Path, c.Type)
		m.answer(res, err)
		return cmd
	case "group":
		res := Result{Req: c.Req, Cmd: c.Cmd, Name: c.ID}
		m.answer(res, m.os.SetMultifocus(c.ID, c.On))
	case "action":
		res := Result{Req: c.Req, Cmd: c.Cmd, Name: c.Name}
		var cmd tea.Cmd
		err := checkAction(c.Name)
		if err == nil {
			err = m.splitRefusal(c.Name)
		}
		if err == nil {
			cmd, err = m.os.RunActionCmd(c.Name)
		}
		m.answer(res, err)
		return cmd
	case "layout":
		res := Result{Req: c.Req, Cmd: c.Cmd, Op: c.Op}
		got, err := runLayout(m.os, c)
		if got != nil {
			res.Ratio = got.Ratio
		}
		m.answer(res, err)
	case "verb":
		nonce := ""
		if personVerbs[c.Verb] && m.nonce != nil {
			nonce = m.nonce()
		}
		if c.Verb == "reply-approval" {
			// This client answers it: the close that follows is not news.
			id, _ := c.Params["request_id"].(string)
			m.os.NoteInboxReplied(id)
		}
		go runVerb(m.out, m.version, nonce, c)
	case "probe-host":
		// The renderer typed into a pane on the host: probe it now.
		pokeHost(c.Name)
	case "quit":
		return tea.Quit
	}
	return nil
}

// answer queues a command's result, sent after the state that shows it.
func (m *model) answer(res Result, err error) {
	res.OK = err == nil
	if err != nil {
		res.Error = err.Error()
	}
	m.results = append(m.results, res)
}

func (m *model) export() {
	o := m.os
	view := o.CurrentSessionView()
	st := &State{
		// The model's own name for the session, which follows a switch made
		// inside the session (a pane running tuios switch, say).
		Session:        o.SessionName,
		Cols:           o.GetRenderWidth(),
		Rows:           o.Height,
		Workspace:      o.CurrentWorkspace,
		NumWorkspaces:  o.NumWorkspaces,
		WorkspaceNames: o.WorkspaceNames,
		Tiling:         o.AutoTiling,
		Windows:        make([]Window, 0, len(o.Windows)),
		Layout:         o.LayoutName(),
		Trees:          exportTrees(o),
		SessionSize:    Size{Cols: view.Cols, Rows: view.Rows},
		ClientSize:     Size{Cols: view.ClientCols, Rows: view.ClientRows},
		AgentSeen:      o.AgentsSeen(),
	}
	if view.Cropped {
		st.Viewport = &Point{X: view.OffsetX, Y: view.OffsetY}
	}
	if ids := o.MultifocusIDs(); len(ids) > 0 {
		st.Multifocus = ids
	}
	if id, corner := o.PiPWindow(); id != "" {
		st.PiP = &PiP{Window: id, Corner: corner}
	}
	if box, ok := o.ScratchBox(); ok {
		r := rectOf(box)
		st.ScratchBox = &r
		st.ScratchOver = o.ScratchOver()
	}
	if vp, width, panes, ok := o.ScrollStrip(); ok {
		strip := &Strip{Viewport: vp, Width: width, Panes: make([]StripPane, 0, len(panes))}
		for _, p := range panes {
			strip.Panes = append(strip.Panes, StripPane{ID: p.ID, X: p.X, Y: p.Y, W: p.W, H: p.H})
		}
		st.Strip = strip
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
		if w.Zoomed {
			st.Zoomed = w.ID
		}
		st.Windows = append(st.Windows, Window{
			ID: w.ID, PTY: w.PTYID, Kind: windowKind(w.Kind), URI: w.URI, Title: w.Title(), Name: w.CustomName,
			Popup: w.IsPopup, Scratch: w.IsScratch, Stack: w.Stack, StackIndex: w.StackIndex,
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

// tap forwards the panes' streams to the renderer, in order. resume and
// from are the positions the renderer's kept emulators hold; see resume.go.
type tap struct {
	out    *frameWriter
	mu     sync.Mutex
	resume map[string]int64
	from   map[string]int64
}

func (t *tap) Snapshot(ptyID string, state *session.TerminalState) {
	if t.keep(ptyID, state) {
		return
	}
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
