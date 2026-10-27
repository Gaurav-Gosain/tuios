package app

import (
	"image"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/charmbracelet/x/ansi"
)

// The rail's pane section: a live terminal pane drawn in the rail, under its
// own heading, for programs that want a small permanent spot (a clock, a
// player, a picture-in-picture viewer such as tuiostv).
//
// # Where the pane lives
//
// It is a window of the attached session, marked Rail with the command line
// the config gave it, on a scratch-range workspace that no client shows. The
// daemon owns the process, so a detach leaves it running and a reattach finds
// it. Being on a workspace nobody is on keeps it out of the tiler, the
// layouts, the dock and the workspace lists with no check in any of them; the
// lists that walk every window (the terminals section, the navigator, the
// go-to picker, list-windows) leave it out by its mark. See
// session/verb_rail_pane.go.
//
// One per session, keyed by the command line. A client whose config names the
// same line keeps the pane there is; a different line replaces it. The
// client asks for the pane when it has none and its program has not stopped:
// a program that exits closes the pane, and the section then says it stopped
// and offers a click to start it again, so a command that fails at once
// cannot loop.
//
// # Size and visibility
//
// The rail lays the section out like any other. The pane is then sized to the
// rail's content columns and the lines the section was given, which reaches
// the program as a resize. While the rail is hidden, collapsed, too narrow or
// gives the section no lines, the client stops the pane's output stream: the
// program runs on in the daemon at its last size, and this client draws
// nothing and wakes for nothing.
//
// # What it costs
//
// The rail's render cache folds in gen, which moves only when the pane had
// output, so an idle program costs no frame and no rebuild. The pane's own
// rendered cells are the cached renderTerminal path every pane uses.
//
// # Input
//
// The pane takes the keyboard through the docked session's path
// (SidebarSessionWindow), as focus target railPaneFocus: a click on it or
// focus_rail_pane gives it the keys, alt+esc, a click elsewhere or a leader
// chord takes them back. A program that asked for the mouse gets clicks,
// motion and the wheel in its own coordinates.

// railPaneFocus is the sidebarSessionFocus value for the rail pane. 1 and 2
// are the docked sessions on the two edges.
const railPaneFocus = 3

// railPaneCreateLimit is how long a create may be on its way before another
// may be asked for. A create the daemon accepted arrives as a state push well
// inside it.
const railPaneCreateLimit = 10 * time.Second

// railPaneState is this client's view of the rail pane.
type railPaneState struct {
	// key is the command line this client's config names.
	key string
	// configured says the config named a pane at the last sync, so a reload
	// that drops it can close the pane this client asked for.
	configured bool
	// pending is a create on its way, since pendingAt.
	pending   bool
	pendingAt time.Time
	// windowID is the pane the client last saw. When it goes away without
	// this client closing it, the program stopped.
	windowID string
	// stopped says the program exited. Nothing restarts it until a click or
	// a config change.
	stopped bool
	// err is why the last create failed, shown in the section.
	err string
	// gen counts output the rail has not drawn yet. The rail's render cache
	// folds it.
	gen uint64
	// cols and rows are the size the last rail layout gave the pane, and
	// rect where its cells were drawn. All zero when the section was not
	// drawn.
	cols, rows int
	rect       image.Rectangle
}

// isRailPane reports whether w is the session's rail pane.
func isRailPane(w *terminal.Window) bool {
	return w != nil && w.Rail != ""
}

// hiddenFromLists reports whether w stays out of the lists a person reads:
// the scratch terminal and the rail pane.
func hiddenFromLists(w *terminal.Window) bool {
	return isScratch(w) || isRailPane(w)
}

// railPaneConfig is the [appearance.sidebar.pane] table in force.
func (m *OS) railPaneConfig() config.SidebarPaneConfig {
	if m.UserConfig == nil {
		return config.SidebarPaneConfig{}
	}
	return m.UserConfig.Appearance.Sidebar.Pane
}

// railPaneEnabled reports whether the rail being drawn has a pane section
// with a command. The pane is drawn on the main rail only: one pane has one
// size, and the second rail would fight the first over it.
func (m *OS) railPaneEnabled() bool {
	return !m.sidebarDrawing && m.railPaneConfig().CommandLine() != "" &&
		sidebarLayoutHas(sidebarSectionPane, &m.Settings)
}

// railPaneWindow is the session's rail pane, or nil.
func (m *OS) railPaneWindow() *terminal.Window {
	for _, w := range m.Windows {
		if isRailPane(w) {
			return w
		}
	}
	return nil
}

// railPaneShown reports whether the pane is on the screen: the main rail is
// up and wide enough, and its last layout gave the section lines.
func (m *OS) railPaneShown() bool {
	if !m.railPaneEnabled() || m.railPaneView.rows <= 0 || m.railPaneView.cols < config.SidebarPaneMinCols {
		return false
	}
	if m.GetSidebarWidth()-1 < config.SidebarPaneMinCols || m.sidebarSessionVisible(m.legacySidebarEdge()) {
		return false
	}
	return true
}

// railPaneKeepsStream reports whether w's output stream has to stay open
// because the rail draws it. See unsubscribeFromPTY.
func (m *OS) railPaneKeepsStream(w *terminal.Window) bool {
	return isRailPane(w) && m.railPaneShown()
}

// RailPaneAt reports whether the screen cell x, y is on the rail pane as it
// was last drawn.
func (m *OS) RailPaneAt(x, y int) bool {
	x, y = m.ScreenPoint(x, y)
	return m.railPaneShown() && m.railPaneWindow() != nil && image.Pt(x, y).In(m.railPaneView.rect)
}

// railPaneNoteOutput is MarkTerminalsWithNewContent's call for a pane off
// the current workspace that had output. It reports whether the pane is the
// rail pane on the screen, which then needs a frame.
func (m *OS) railPaneNoteOutput(w *terminal.Window) bool {
	if !isRailPane(w) || !m.railPaneShown() {
		return false
	}
	w.MarkContentDirty()
	m.railPaneView.gen++
	m.cachedViewContent = ""
	return true
}

// TouchRailPane redraws the rail pane after something other than its
// output changed what it shows, such as a scroll of its history. Any other
// window is left alone.
func (m *OS) TouchRailPane(w *terminal.Window) {
	if !isRailPane(w) {
		return
	}
	w.MarkContentDirty()
	m.railPaneView.gen++
	m.cachedViewContent = ""
}

// RailPaneSyncCmd is the one place the client decides what the rail pane
// needs. It runs once per message from Update, after the handler, like
// FilesSyncCmd, and answers nil after a few comparisons on an idle client.
func (m *OS) RailPaneSyncCmd() tea.Cmd {
	cfg := m.railPaneConfig()
	key := cfg.CommandLine()
	want := key != "" && sidebarLayoutHas(sidebarSectionPane, &m.Settings)
	w := m.railPaneWindow()

	if !want {
		m.railPaneView.cols, m.railPaneView.rows, m.railPaneView.rect = 0, 0, image.Rectangle{}
		if w != nil {
			m.railPaneHide(w)
		}
		// Only a client whose own config named the pane closes it, and only
		// when that config changes: a client attached with no pane in its
		// config leaves another client's pane alone.
		if m.railPaneView.configured {
			m.railPaneView = railPaneState{}
			if w != nil {
				return m.railPaneCloseCmd(w)
			}
		}
		return nil
	}
	if m.railPaneView.key != key {
		m.railPaneView.key = key
		m.railPaneView.stopped, m.railPaneView.err, m.railPaneView.windowID = false, "", ""
	}
	m.railPaneView.configured = true

	if w == nil || w.Rail != key {
		if w == nil && m.railPaneView.windowID != "" && !m.railPaneView.pending {
			// The pane this client saw is gone and nobody here closed it:
			// its program stopped.
			m.railPaneView.stopped = true
			m.sidebarCache.invalidate()
		}
		if w == nil {
			m.railPaneView.windowID = ""
		}
		if m.railPaneView.pending && time.Since(m.railPaneView.pendingAt) < railPaneCreateLimit {
			return nil
		}
		m.railPaneView.pending = false
		if m.railPaneView.stopped || m.railPaneView.err != "" {
			return nil
		}
		return m.railPaneCreateCmd(key)
	}
	if m.railPaneView.windowID != w.ID {
		m.railPaneView.windowID = w.ID
		m.railPaneView.pending = false
		m.sidebarCache.invalidate()
	}
	m.railPaneApply()
	return nil
}

// railPaneApply puts the pane where the last rail layout drew it: the size
// it was given, its output streamed while it is on the screen and not while
// it is off it. It acts only on a change, so it is safe to call from the
// rail's draw as well as from the sync.
func (m *OS) railPaneApply() {
	w := m.railPaneWindow()
	if w == nil {
		return
	}
	if !m.railPaneShown() {
		m.railPaneHide(w)
		return
	}
	// The window's box is the rectangle with a border around it, so its
	// content is exactly the rectangle. The border is never drawn; it is
	// there because the bordered box is the shape every resize path keeps.
	r := m.railPaneView.rect
	if w.X != r.Min.X-1 || w.Y != r.Min.Y-1 {
		w.X, w.Y = r.Min.X-1, r.Min.Y-1
	}
	if w.Width != m.railPaneView.cols+2 || w.Height != m.railPaneView.rows+2 || w.ContentWidth() != m.railPaneView.cols {
		w.Resize(m.railPaneView.cols+2, m.railPaneView.rows+2)
		m.railPaneView.gen++
	}
	if m.IsDaemonSession && w.DaemonMode && w.PTYID != "" && !m.SubscribedPTYs[w.PTYID] {
		// Fetches the daemon's screen and starts the stream. The stream then
		// owns the emulator's size, so the resize above reaches it from the
		// daemon at the byte the program saw it.
		m.primePaneFromDaemon(w)
		w.MarkContentDirty()
		m.railPaneView.gen++
	}
}

// railPaneHide stops the pane's stream while it is off the screen. The
// program keeps running in the daemon.
func (m *OS) railPaneHide(w *terminal.Window) {
	if m.SidebarSessionWindow() == w {
		m.BlurSidebarSession()
	}
	if m.IsDaemonSession && w.DaemonMode && w.PTYID != "" && m.SubscribedPTYs[w.PTYID] {
		m.unsubscribeFromPTY(w)
	}
}

// RailPaneOpenedMsg reports the call that asked the daemon for the pane.
type RailPaneOpenedMsg struct {
	Key string
	Err error
}

// railPaneRequest is what the daemon call needs, copied off the model.
type railPaneRequest struct {
	Session, Host, Build string
	Key                  string
	Command              []string
	Dir                  string
	Width, Height        int
	Off                  bool
}

// railPaneCaller makes the call. A variable so a test can stand in for the
// daemon.
var railPaneCaller = callRailPane

func callRailPane(req railPaneRequest) error {
	c, err := dialSessionDaemon(req.Host, req.Build)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	params := map[string]any{"session": req.Session}
	if req.Off {
		params["off"] = true
	} else {
		params["key"] = req.Key
		params["command"] = req.Command
		params["width"] = req.Width
		params["height"] = req.Height
		if req.Dir != "" {
			params["cwd"] = req.Dir
		}
	}
	_, err = c.Call("rail-pane", params)
	return err
}

// railPaneStartSize is the size the program starts at: the last layout's
// when the section has been drawn, else the rail's content width and the
// configured height.
func (m *OS) railPaneStartSize() (int, int) {
	if m.railPaneView.cols > 0 && m.railPaneView.rows > 0 {
		return m.railPaneView.cols, m.railPaneView.rows
	}
	cols := max(m.GetSidebarWidth()-1, config.SidebarPaneMinCols)
	rows := m.railPaneConfig().Height
	if rows <= 0 {
		rows = 10
	}
	return cols, rows
}

// railPaneCreateCmd asks for the pane. In a session with no daemon the
// client starts it itself.
func (m *OS) railPaneCreateCmd(key string) tea.Cmd {
	cols, rows := m.railPaneStartSize()
	if !m.IsDaemonSession || m.DaemonClient == nil {
		m.createLocalRailPane(key, cols, rows)
		return nil
	}
	dir := ""
	if m.AttachedHost == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dir = home
		}
	}
	m.railPaneView.pending = true
	m.railPaneView.pendingAt = time.Now()
	req := railPaneRequest{
		Session: m.SessionName, Host: m.AttachedHost, Build: m.DaemonClient.ClientVersion(),
		Key: key, Command: commandArgv(key, nil), Dir: dir, Width: cols, Height: rows,
	}
	return func() tea.Msg {
		return RailPaneOpenedMsg{Key: key, Err: railPaneCaller(req)}
	}
}

// railPaneCloseCmd closes the pane this client's config no longer names.
func (m *OS) railPaneCloseCmd(w *terminal.Window) tea.Cmd {
	if !m.IsDaemonSession || m.DaemonClient == nil {
		if i := m.windowIndexByID(w.ID); i >= 0 {
			m.DeleteWindow(i)
		}
		return nil
	}
	req := railPaneRequest{Session: m.SessionName, Host: m.AttachedHost, Build: m.DaemonClient.ClientVersion(), Off: true}
	return func() tea.Msg {
		return RailPaneOpenedMsg{Err: railPaneCaller(req)}
	}
}

// handleRailPaneOpened records a failed create, which the section shows. A
// create the daemon accepted arrives as a state push.
func (m *OS) handleRailPaneOpened(msg RailPaneOpenedMsg) {
	if msg.Key == "" || msg.Key != m.railPaneView.key {
		return
	}
	if msg.Err == nil {
		return
	}
	m.railPaneView.pending = false
	why := m.sessionCallFailure(msg.Err)
	if why == "" {
		why = plainCause(msg.Err)
	}
	m.railPaneView.err = why
	m.sidebarCache.invalidate()
}

// RestartRailPane is the click on a stopped or failed section: it forgets
// the stop and asks for the pane again.
func (m *OS) RestartRailPane() {
	// The sync that runs after this message asks for it.
	m.railPaneView.stopped, m.railPaneView.err, m.railPaneView.pending = false, "", false
	m.sidebarCache.invalidate()
}

// createLocalRailPane starts the pane in a session without a daemon, on a
// workspace nobody is on.
func (m *OS) createLocalRailPane(key string, cols, rows int) {
	ws := session.ScratchWorkspaceBase
	for used := true; used; {
		used = false
		for _, w := range m.Windows {
			if w.Workspace == ws {
				used = true
				ws++
				break
			}
		}
	}
	dir, _ := os.UserHomeDir()
	w, err := terminal.NewWindowIn(dir, createID(), "rail pane", 0, 0, cols+2, rows+2, len(m.Windows),
		m.WindowExitChan, m.PTYDataChan, m.Settings.ScrollbackLines, commandArgv(key, nil)...)
	if err != nil {
		m.railPaneView.err = plainCause(err)
		m.sidebarCache.invalidate()
		return
	}
	w.Workspace = ws
	w.Rail = key
	m.installPassthroughs(w)
	m.Windows = append(m.Windows, w)
	m.railPaneView.windowID = w.ID
}

// FocusRailPane gives the rail pane the keyboard, the focus_rail_pane key.
// It reports whether there was a pane on the screen to give it to.
func (m *OS) FocusRailPane() bool {
	if m.railPaneWindow() == nil || !m.railPaneShown() {
		return false
	}
	m.FocusSidebarSession(railPaneFocus)
	return true
}

// RailPaneFocused reports whether the rail pane has the keyboard.
func (m *OS) RailPaneFocused() bool { return m.sidebarSessionFocus == railPaneFocus }

// railPaneNote is the one row the section draws when there is no pane to
// show, and whether a click on it starts the pane.
func (m *OS) railPaneNote() (string, bool) {
	switch {
	case m.railPaneView.err != "":
		return "Did not start: " + m.railPaneView.err + " Click to try again.", true
	case m.railPaneView.stopped:
		return "Stopped. Click to start it again.", true
	}
	return "Starting…", false
}

// railPaneLines is the pane's cells as rail lines, each exactly cols wide,
// rows of them, from the same cached render a normal pane uses. A line the
// pane drew wider is cut and a shorter one padded. Cells with no background
// of their own stay without one, so the rail's ground shows through them.
func (m *OS) railPaneLines(w *terminal.Window, cols, rows int) []string {
	focused := m.RailPaneFocused()
	lines := strings.Split(m.renderTerminal(w, focused, focused), "\n")
	if len(lines) > rows {
		lines = lines[:rows]
	}
	for len(lines) < rows {
		lines = append(lines, "")
	}
	for i, line := range lines {
		width := ansi.StringWidth(line)
		if width > cols {
			line = ansi.Truncate(line, cols, "")
			width = ansi.StringWidth(line)
		}
		// The pane's last style and link must not reach the padding or the
		// rail's edge rule.
		lines[i] = line + "\x1b[m\x1b]8;;\x1b\\" + strings.Repeat(" ", max(cols-width, 0))
	}
	return lines
}
