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

const railPaneFocus = 3

const railPaneCreateLimit = 10 * time.Second

type railPaneState struct {
	key        string
	configured bool
	pending    bool
	pendingAt  time.Time
	windowID   string
	stopped    bool
	err        string
	gen        uint64
	noted      uint64
	cols, rows int
	rect       image.Rectangle
}

func isRailPane(w *terminal.Window) bool {
	return w != nil && w.Rail != ""
}

func hiddenFromLists(w *terminal.Window) bool {
	return isScratch(w) || isRailPane(w)
}

func (m *OS) railPaneConfig() config.SidebarPaneConfig {
	if m.UserConfig == nil {
		return config.SidebarPaneConfig{}
	}
	return m.UserConfig.Appearance.Sidebar.Pane
}

func (m *OS) railPaneEnabled() bool {
	return !m.sidebarDrawing && m.railPaneConfig().CommandLine() != "" &&
		sidebarLayoutHas(sidebarSectionPane, &m.Settings)
}

func (m *OS) railPaneWindow() *terminal.Window {
	for _, w := range m.Windows {
		if isRailPane(w) {
			return w
		}
	}
	return nil
}

func (m *OS) railPaneShown() bool {
	if !m.railPaneEnabled() || m.railPaneView.rows <= 0 || m.railPaneView.cols < config.SidebarPaneMinCols {
		return false
	}
	if m.GetSidebarWidth()-1 < config.SidebarPaneMinCols || m.sidebarSessionVisible(m.legacySidebarEdge()) {
		return false
	}
	return true
}

func (m *OS) railPaneKeepsStream(w *terminal.Window) bool {
	return isRailPane(w) && m.railPaneShown()
}

// RailPaneAt reports whether the screen cell x, y is on the rail pane as it
// was last drawn.
func (m *OS) RailPaneAt(x, y int) bool {
	x, y = m.ScreenPoint(x, y)
	return m.railPaneShown() && m.railPaneWindow() != nil && image.Pt(x, y).In(m.railPaneView.rect)
}

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

func (m *OS) railPaneApply() {
	w := m.railPaneWindow()
	if w == nil {
		return
	}
	if !m.railPaneShown() {
		m.railPaneHide(w)
		return
	}
	r := m.railPaneView.rect
	if w.X != r.Min.X-1 || w.Y != r.Min.Y-1 {
		w.X, w.Y = r.Min.X-1, r.Min.Y-1
	}
	if w.Width != m.railPaneView.cols+2 || w.Height != m.railPaneView.rows+2 || w.ContentWidth() != m.railPaneView.cols {
		w.Resize(m.railPaneView.cols+2, m.railPaneView.rows+2)
		m.railPaneView.gen++
	}
	if m.IsDaemonSession && w.DaemonMode && w.PTYID != "" && !m.SubscribedPTYs[w.PTYID] {
		m.primePaneFromDaemon(w)
		w.MarkContentDirty()
		m.railPaneView.gen++
	}
}

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

type railPaneRequest struct {
	Session, Host, Build string
	Key                  string
	Command              []string
	Dir                  string
	Width, Height        int
	Off                  bool
}

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
	m.railPaneView.stopped, m.railPaneView.err, m.railPaneView.pending = false, "", false
	m.sidebarCache.invalidate()
}

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
	caps := m.hostCaps()
	w, err := terminal.NewWindowInCell(dir, createID(), "rail pane", 0, 0, cols+2, rows+2, len(m.Windows),
		m.WindowExitChan, m.PTYDataChan, m.Settings.ScrollbackLines, caps.CellWidth, caps.CellHeight, commandArgv(key, nil)...)
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

func (m *OS) railPaneNote() (string, bool) {
	switch {
	case m.railPaneView.err != "":
		return "Did not start: " + m.railPaneView.err + " Click to try again.", true
	case m.railPaneView.stopped:
		return "Stopped. Click to start it again.", true
	}
	return "Starting…", false
}

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
		lines[i] = line + "\x1b[m\x1b]8;;\x1b\\" + strings.Repeat(" ", max(cols-width, 0))
	}
	return lines
}
