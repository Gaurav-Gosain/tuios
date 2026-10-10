package app

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/charmbracelet/x/ansi"
)

// A rail session is a second *client* of the daemon, not a shell owned by the
// center client. Its state and subscriptions must never enter OS.Windows: a
// center-session state sync replaces that slice, and leaving this client must
// not close the processes in either of the docked sessions.
type sidebarSessionView struct {
	name       string
	generation uint64
	client     *session.TUIClient
	window     *terminal.Window // daemon-active pane; the focus may be another visible pane
	ptyID      string
	panes      []session.WindowState // ordered visible panes, at least four rows each
	windows    map[string]*terminal.Window
	pending    map[string]bool
	focusPTY   string
	workspace  int
	paneIndex  int
	paneCount  int
	width      int
	height     int
	problem    string
}

type sidebarSessionEvent struct {
	edge       sidebarEdge
	generation uint64
	kind       string
	client     *session.TUIClient
	state      *session.SessionState
	pane       *session.WindowState
	terminal   *session.TerminalState
	err        error
}

type sidebarSessionNotice struct{ sidebarSessionEvent }

// A failed rail attachment is retried without polling healthy, idle sessions.
// The generation makes a retry harmless after a reassignment or a hidden rail.
func (m *OS) retrySidebarSession(edge sidebarEdge) tea.Cmd {
	generation := m.sidebarSessions[edge].generation
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg {
		return sidebarSessionEvent{edge: edge, generation: generation, kind: "retry"}
	})
}

func (m *OS) sidebarSessionConfig(edge sidebarEdge) *config.SidebarEdgeConfig {
	if m.UserConfig == nil {
		return nil
	}
	if edge == sidebarLeft {
		return m.UserConfig.Appearance.Sidebar.Left
	}
	return m.UserConfig.Appearance.Sidebar.Right
}

func (m *OS) sidebarSessionWidth(edge sidebarEdge) int {
	if edge == m.legacySidebarEdge() {
		return m.GetSidebarWidth()
	}
	return m.secondarySidebarWidth()
}

func (m *OS) sidebarSessionVisible(edge sidebarEdge) bool {
	cfg := m.sidebarSessionConfig(edge)
	return m.DaemonClient != nil && cfg != nil && cfg.Session != "" && m.sidebarEdgeEnabled(edge) &&
		(cfg.SessionWorkspace == 0 || cfg.SessionWorkspace == m.CurrentWorkspace) &&
		m.sidebarSessionWidth(edge) >= 8 && m.ViewUsableHeight() >= 4
}

// refreshSidebarSessions reconciles both independent attachments with the
// current config, workspace and geometry. An inactive rail disconnects its
// viewing client, not the daemon session whose process it was displaying.
func (m *OS) refreshSidebarSessions() tea.Cmd {
	var cmds []tea.Cmd
	for _, edge := range []sidebarEdge{sidebarLeft, sidebarRight} {
		v := &m.sidebarSessions[edge]
		cfg := m.sidebarSessionConfig(edge)
		name := ""
		if m.sidebarSessionVisible(edge) {
			name = cfg.Session
		}
		width, height := m.sidebarSessionWidth(edge), m.ViewUsableHeight()
		if v.name != name || name == "" {
			if v.name != name {
				m.closeSidebarSession(edge)
				v = &m.sidebarSessions[edge]
				v.name, v.width, v.height = name, width, height
				if name != "" {
					v.generation++
					cmds = append(cmds, m.connectSidebarSession(edge, v.generation, name, width, height))
				}
			}
			continue
		}
		if v.width != width || v.height != height {
			v.width, v.height = width, height
			m.layoutSidebarSessionPanes(edge)
			if v.client != nil {
				client := v.client
				cmds = append(cmds, func() tea.Msg { _ = client.NotifyTerminalSize(width, height); return nil })
			}
			m.MarkAllDirty()
		}
	}
	return tea.Batch(cmds...)
}

// settleSidebarSessionSizes announces only the last size of a host resize
// burst to each guest; visual-only steps have already moved their frames.
func (m *OS) settleSidebarSessionSizes() {
	for _, edge := range []sidebarEdge{sidebarLeft, sidebarRight} {
		v := &m.sidebarSessions[edge]
		if m.sidebarSessionVisible(edge) {
			for _, pane := range v.panes {
				if w := v.windows[pane.PTYID]; w != nil {
					w.Resize(w.Width, w.Height)
				}
			}
		}
	}
}

func (m *OS) closeSidebarSession(edge sidebarEdge) {
	v := &m.sidebarSessions[edge]
	v.generation++ // invalidate any connect/snapshot still in flight
	if v.client != nil {
		_ = v.client.Close() // detaches only this viewer, not the daemon session
		v.client = nil
	}
	for _, w := range v.windows {
		w.Close()
	}
	v.window, v.windows, v.pending, v.panes = nil, nil, nil, nil
	v.name, v.ptyID, v.focusPTY, v.problem = "", "", "", ""
	if m.sidebarSessionFocus == int(edge)+1 {
		m.sidebarSessionFocus = 0
	}
	m.MarkAllDirty()
}

func (m *OS) connectSidebarSession(edge sidebarEdge, generation uint64, name string, width, height int) tea.Cmd {
	version := m.DaemonClient.ClientVersion()
	return func() tea.Msg {
		client := session.NewTUIClient()
		client.Passive = true
		if err := client.Connect(version, width, height); err != nil {
			_ = client.Close()
			return sidebarSessionEvent{edge: edge, generation: generation, kind: "connect", err: err}
		}
		state, err := client.AttachSession(name, false, width, height)
		if err != nil {
			_ = client.Close()
			return sidebarSessionEvent{edge: edge, generation: generation, kind: "connect", err: err}
		}
		return sidebarSessionEvent{edge: edge, generation: generation, kind: "connect", client: client, state: state}
	}
}

func (m *OS) queueSidebarSessionEvent(event sidebarSessionEvent) {
	if m.sidebarSessionEvents == nil {
		return
	}
	select {
	case m.sidebarSessionEvents <- event:
	default:
		// A later state sync contains the whole session, not a delta. The next
		// subscription update or reconnect will redraw if the queue is full.
	}
}

func (m *OS) listenSidebarSession() tea.Cmd {
	// Capture the shutdown channel before starting the async command. Stop
	// closes it and clears the OS field; reading that field later could race
	// with shutdown or leave a listener parked on a nil channel forever.
	events, done := m.sidebarSessionEvents, m.sidebarSessionDone
	return func() tea.Msg {
		select {
		case event := <-events:
			return sidebarSessionNotice{event}
		case <-done:
			return nil
		}
	}
}

func sidebarActivePane(state *session.SessionState) *session.WindowState {
	if state == nil {
		return nil
	}
	for i := range state.Windows {
		w := &state.Windows[i]
		if w.ID == state.FocusedWindowID && w.Workspace == state.CurrentWorkspace && !w.Minimized && w.PTYID != "" {
			return w
		}
	}
	for i := range state.Windows {
		w := &state.Windows[i]
		if w.Workspace == state.CurrentWorkspace && !w.Minimized && w.PTYID != "" {
			return w
		}
	}
	return nil
}

// sidebarPaneSlots partitions a rail's body into vertical framed panes. If
// the rail cannot give every pane six rows (four guest rows), show a contiguous group that
// contains the daemon-active pane; the header still reports the full count.
func (m *OS) layoutSidebarSessionPanes(edge sidebarEdge) {
	v := &m.sidebarSessions[edge]
	count := len(v.panes)
	if count == 0 {
		return
	}
	body := max(1, v.height)
	y := m.viewReserve().Top
	for i, pane := range v.panes {
		h := body / count
		if i < body%count {
			h++
		}
		if w := v.windows[pane.PTYID]; w != nil {
			w.X, w.Y = sidebarEdgeX(edge, v.width, m.GetRenderWidth()), y
			if m.viewportResizing {
				w.ResizeVisual(v.width, h)
			} else {
				w.Resize(v.width, h)
			}
		}
		y += h
	}
}

func (m *OS) applySidebarSessionState(edge sidebarEdge, state *session.SessionState) tea.Cmd {
	v := &m.sidebarSessions[edge]
	if v.windows == nil {
		v.windows = make(map[string]*terminal.Window)
		v.pending = make(map[string]bool)
	}
	active := sidebarActivePane(state)
	var eligible []session.WindowState
	v.paneIndex, v.paneCount = 0, 0
	if state != nil {
		v.workspace = state.CurrentWorkspace
		for _, pane := range state.Windows {
			if pane.Workspace == state.CurrentWorkspace && !pane.Minimized && pane.PTYID != "" {
				eligible = append(eligible, pane)
				if active != nil && pane.ID == active.ID {
					v.paneIndex = len(eligible)
				}
			}
		}
	}
	v.paneCount = len(eligible)
	capacity := max(1, v.height/6)
	if len(eligible) > capacity {
		start := max(0, v.paneIndex-capacity)
		start = min(start, len(eligible)-capacity)
		eligible = eligible[start : start+capacity]
	}
	visible := make(map[string]bool, len(eligible))
	for _, pane := range eligible {
		visible[pane.PTYID] = true
	}
	for id, w := range v.windows {
		if !visible[id] {
			v.client.UnsubscribePTY(id)
			w.Close()
			delete(v.windows, id)
		}
	}
	for id := range v.pending {
		if !visible[id] {
			delete(v.pending, id)
		}
	}
	v.panes = eligible
	v.ptyID, v.window = "", nil
	if active != nil {
		v.ptyID = active.PTYID
		v.window = v.windows[v.ptyID]
	}
	// Daemon navigation changes the active pane without a mouse click. Keep
	// the rail's keyboard target aligned with that active pane on state sync.
	if m.sidebarSessionFocus == int(edge)+1 || !visible[v.focusPTY] {
		v.focusPTY = v.ptyID
	}
	m.layoutSidebarSessionPanes(edge)
	var cmds []tea.Cmd
	for _, pane := range eligible {
		if w := v.windows[pane.PTYID]; w != nil {
			if w.Title() != pane.Title {
				w.SetTitle(pane.Title)
			}
			w.ForegroundCmd = pane.ForegroundCmd
			continue
		}
		if v.pending[pane.PTYID] {
			continue
		}
		v.pending[pane.PTYID] = true
		client, generation, copyPane := v.client, v.generation, pane
		scrollback := m.Settings.ScrollbackLines
		cmds = append(cmds, func() tea.Msg {
			snapshot, err := client.GetTerminalState(copyPane.PTYID, scrollback, 0)
			return sidebarSessionEvent{edge: edge, generation: generation, kind: "snapshot", pane: &copyPane, terminal: snapshot, err: err}
		})
	}
	m.MarkAllDirty()
	return tea.Batch(cmds...)
}

func (m *OS) handleSidebarSessionEvent(event sidebarSessionEvent) tea.Cmd {
	v := &m.sidebarSessions[event.edge]
	if event.generation != v.generation || !m.sidebarSessionVisible(event.edge) {
		if event.client != nil {
			_ = event.client.Close()
		}
		return nil
	}
	switch event.kind {
	case "retry":
		if v.client == nil {
			m.closeSidebarSession(event.edge)
			return m.refreshSidebarSessions()
		}
	case "connect":
		if event.err != nil {
			v.problem = event.err.Error()
			m.MarkAllDirty()
			return m.retrySidebarSession(event.edge)
		}
		v.client = event.client
		client, edge, generation := v.client, event.edge, v.generation
		client.OnStateSync(func(state *session.SessionState, _, _ string) {
			m.queueSidebarSessionEvent(sidebarSessionEvent{edge: edge, generation: generation, kind: "state", state: state})
		})
		client.OnSessionEnded(func(_, reason string) {
			m.queueSidebarSessionEvent(sidebarSessionEvent{edge: edge, generation: generation, kind: "ended", err: fmt.Errorf("session ended: %s", reason)})
		})
		client.OnDisconnect(func(err error) {
			m.queueSidebarSessionEvent(sidebarSessionEvent{edge: edge, generation: generation, kind: "ended", err: err})
		})
		client.StartReadLoop()
		return m.applySidebarSessionState(edge, event.state)
	case "state":
		if v.client != nil {
			return m.applySidebarSessionState(event.edge, event.state)
		}
	case "snapshot":
		if v.client == nil || event.pane == nil || !v.pending[event.pane.PTYID] {
			return nil
		}
		delete(v.pending, event.pane.PTYID)
		if event.err != nil {
			m.closeSidebarSession(event.edge)
			v.name = m.sidebarSessionConfig(event.edge).Session
			v.problem = event.err.Error()
			m.MarkAllDirty()
			return m.retrySidebarSession(event.edge)
		}
		ptyID, client := event.pane.PTYID, v.client
		w := terminal.NewDaemonWindow(event.pane.ID, event.pane.Title,
			sidebarEdgeX(event.edge, v.width, m.GetRenderWidth()), m.viewReserve().Top,
			v.width, v.height, 0, ptyID, m.PTYDataChan, m.Settings.ScrollbackLines)
		w.ForegroundCmd = event.pane.ForegroundCmd
		w.DaemonWriteFunc = func(data []byte) error { return client.WritePTY(ptyID, data) }
		w.DaemonResizeFunc = func(width, height int) error { return client.ResizePTY(ptyID, width, height) }
		w.StartDaemonResponseReader()
		w.SetStreamOwnsSize(true)
		w.ResizeEmulatorToSnapshot(event.terminal.Width, event.terminal.Height)
		m.restoreTerminalContent(w, event.terminal)
		v.windows[ptyID] = w
		if ptyID == v.ptyID {
			v.window = w
		}
		v.client.OnPTYResized(ptyID, w.ResizeFromStream)
		if err := v.client.SubscribePTY(ptyID, event.terminal.Seq, true, w.WriteOutputAsync); err != nil {
			m.closeSidebarSession(event.edge)
			v.name = m.sidebarSessionConfig(event.edge).Session
			v.problem = err.Error()
			return m.retrySidebarSession(event.edge)
		} else {
			// Detached sessions may still have their 80x24 startup PTY.
			// Each visible guest gets its own share of the rail height.
			m.layoutSidebarSessionPanes(event.edge)
			w.Resize(w.Width, w.Height) // attachment must announce even during an initial viewport settle
		}
		m.MarkAllDirty()
	case "ended":
		m.closeSidebarSession(event.edge)
		v.name = m.sidebarSessionConfig(event.edge).Session // show the failure, not an empty rail
		if event.err != nil {
			v.problem = event.err.Error()
		} else {
			v.problem = "session disconnected"
		}
		m.MarkAllDirty()
		return m.retrySidebarSession(event.edge)
	}
	return nil
}

// sidebarSessionPanel replaces a rail's ordinary sections when a daemon
// session is assigned. Its visible panes stack vertically in this client;
// they are normal daemon panes, not client-owned shell sections.
func (m *OS) sidebarSessionPanel(edge sidebarEdge, width int) (string, bool) {
	if !m.sidebarSessionVisible(edge) || width == 0 {
		return "", false
	}
	v := &m.sidebarSessions[edge]
	lines := make([]string, m.ViewUsableHeight())
	label := " " + v.name
	if v.paneCount > 1 {
		label += fmt.Sprintf("  %d/%d", v.paneIndex, v.paneCount)
	}
	if cfg := m.sidebarSessionConfig(edge); cfg != nil && cfg.SessionWorkspace > 0 {
		label += fmt.Sprintf("  [%d]", cfg.SessionWorkspace)
	}
	// Keep the selector visible while a pane snapshot is still loading.
	lines[0] = sidebarFit(label, width, m.railGround())
	for i, pane := range v.panes {
		w := v.windows[pane.PTYID]
		if w == nil {
			continue
		}
		focused := m.sidebarSessionFocus == int(edge)+1 && m.SidebarSessionWindow() == w
		border := theme.BorderUnfocusedOn(m.host.bg)
		if focused {
			border = theme.BorderFocusedTerminalOn(m.host.bg)
		}
		body := strings.Split(m.renderWindowBox(w, 0, focused, border), "\n")
		if i == 0 && len(body) > 0 {
			// The selector replaces this pane's title bar, rather than
			// taking a full row above its frame. There are no window
			// buttons in this bar: the entire label is the pane selector.
			body[0] = m.sidebarSessionTitleBar(label, width, border)
			m.recordWindowButtons(w.ID, nil)
		}
		start := w.Y - m.viewReserve().Top
		for y := 0; y < w.Height && y < len(body) && start+y < len(lines); y++ {
			lines[start+y] = sidebarFit(body[y], width, m.railGround())
		}
	}
	if len(v.panes) == 0 && v.problem != "" && len(lines) > 1 {
		lines[1] = sidebarFit(" "+v.problem, width, m.railGround())
	}
	for y := range lines {
		if lines[y] == "" {
			lines[y] = sidebarFit("", width, m.railGround())
		}
	}
	return strings.Join(lines, "\n"), true
}

// sidebarSessionTitleBar puts the rail's daemon session identity in the first
// pane's border. This keeps its guest grid, border and cursor on the same
// geometry as an ordinary pane, with no extra header row above the frame.
func (m *OS) sidebarSessionTitleBar(label string, width int, borderColor color.Color) string {
	left, right, horizontal := m.Settings.GetWindowBorderTopLeft(), m.Settings.GetWindowBorderTopRight(), m.Settings.GetWindowBorderTop()
	inner := max(0, width-lipgloss.Width(left)-lipgloss.Width(right))
	name := ansi.Truncate(" "+printableTitle(label), inner, "")
	return lipgloss.NewStyle().Foreground(borderColor).Render(left + name + strings.Repeat(horizontal, max(0, inner-lipgloss.Width(name))) + right)
}

func (m *OS) sidebarSessionOutputPending() bool {
	for i := range m.sidebarSessions {
		for _, w := range m.sidebarSessions[i].windows {
			if w.HasNewOutput.Load() {
				return true
			}
		}
	}
	return false
}

// SidebarSessionAt claims the body of a docked session's pane, leaving the
// rail's outer edge available for the existing width-resize gesture.
func (m *OS) SidebarSessionAt(x, y int) (edge int, ok bool) {
	x, y = m.ScreenPoint(x, y)
	for _, side := range []sidebarEdge{sidebarLeft, sidebarRight} {
		if !m.sidebarSessionVisible(side) || len(m.sidebarSessions[side].windows) == 0 {
			continue
		}
		w := m.sidebarSessionWidth(side)
		start := sidebarEdgeX(side, w, m.GetRenderWidth())
		border := start + w - 1
		if side == sidebarRight {
			border = start
		}
		if x >= start && x < start+w && x != border && y > m.viewReserve().Top && y < m.viewReserve().Top+m.ViewUsableHeight() {
			return int(side) + 1, true
		}
	}
	return 0, false
}

// SidebarSessionHeaderAt identifies the selector in the first pane's top
// border. Clicking it cycles the daemon's active pane without moving the
// center attachment or borrowing the ordinary rail's session-list hits.
func (m *OS) SidebarSessionHeaderAt(x, y int) (edge int, ok bool) {
	x, y = m.ScreenPoint(x, y)
	if y != m.viewReserve().Top {
		return 0, false
	}
	for _, side := range []sidebarEdge{sidebarLeft, sidebarRight} {
		if !m.sidebarSessionVisible(side) {
			continue
		}
		width := m.sidebarSessionWidth(side)
		start := sidebarEdgeX(side, width, m.GetRenderWidth())
		border := start + width - 1
		if side == sidebarRight {
			border = start
		}
		if x >= start && x < start+width && x != border {
			return int(side) + 1, true
		}
	}
	return 0, false
}

// SidebarSessionPaneAt returns the stacked pane displayed at this screen cell.
func (m *OS) SidebarSessionPaneAt(x, y int) *terminal.Window {
	if edge, ok := m.SidebarSessionAt(x, y); ok {
		_, row := m.ScreenPoint(x, y)
		for _, pane := range m.sidebarSessions[edge-1].panes {
			w := m.sidebarSessions[edge-1].windows[pane.PTYID]
			if w != nil && row >= w.Y && row < w.Y+w.Height {
				return w
			}
		}
	}
	return nil
}

// FocusSidebarSession gives keyboard input to the selected daemon pane. The
// center session remains attached, and clicking its pane returns input to it.
func (m *OS) FocusSidebarSession(edge int) {
	m.sidebarSessionFocus = edge
	m.SidebarFocused = false
	m.MarkAllDirty()
}

func (m *OS) SidebarSessionFocused() bool { return m.sidebarSessionFocus != 0 }

func (m *OS) BlurSidebarSession() {
	if m.sidebarSessionFocus != 0 {
		m.sidebarSessionFocus = 0
		m.MarkAllDirty()
	}
}

func (m *OS) SidebarSessionWindow() *terminal.Window {
	if m.sidebarSessionFocus == 0 {
		return nil
	}
	v := &m.sidebarSessions[m.sidebarSessionFocus-1]
	if w := v.windows[v.focusPTY]; w != nil {
		return w
	}
	return v.window
}

// FocusSidebarSessionPane makes a clicked stacked pane the input target and
// records its daemon focus, without changing the center session attachment.
func (m *OS) FocusSidebarSessionPane(w *terminal.Window) {
	if w == nil || m.sidebarSessionFocus == 0 {
		return
	}
	v := &m.sidebarSessions[m.sidebarSessionFocus-1]
	v.focusPTY = w.PTYID
	if v.client != nil {
		_ = v.client.SendIntent("FocusWindow", w.ID)
	}
	m.MarkAllDirty()
}

func (m *OS) WriteSidebarSession(data []byte) bool {
	if m.sidebarSessionFocus == 0 {
		return false
	}
	v := &m.sidebarSessions[m.sidebarSessionFocus-1]
	w := m.SidebarSessionWindow()
	if v.client == nil || w == nil {
		return false
	}
	return w.SendInput(data) == nil
}

func (m *OS) PasteSidebarSession(text string) bool {
	w := m.SidebarSessionWindow()
	return w != nil && w.Paste(text) == nil
}

// SidebarSessionIntent runs a pane command in the assigned session, without
// mutating the center session's layout or attachment.
func (m *OS) SidebarSessionIntent(command string, args ...string) bool {
	if m.sidebarSessionFocus == 0 {
		return false
	}
	v := &m.sidebarSessions[m.sidebarSessionFocus-1]
	if v.client == nil || m.SidebarSessionWindow() == nil {
		return false
	}
	if err := v.client.SendIntent(command, args...); err != nil {
		m.LogError("Sidebar session %s: %s: %v", v.name, command, err)
		return false
	}
	return true
}

// NewSidebarSessionWindow creates a normal daemon pane in the session whose
// rail currently owns the keyboard, without changing the center attachment.
func (m *OS) NewSidebarSessionWindow() bool {
	if m.sidebarSessionFocus == 0 {
		return false
	}
	v := &m.sidebarSessions[m.sidebarSessionFocus-1]
	if v.client == nil || m.SidebarSessionWindow() == nil {
		return false
	}
	// SendNewWindowFrom is only for an *empty* workspace and deliberately
	// skips when there is already a pane. A normal split needs NewWindow.
	if err := v.client.SendNewWindowIntent("", v.workspace, "", "NewWindow"); err != nil {
		m.LogError("Sidebar session %s: new pane: %v", v.name, err)
		return false
	}
	return true
}

func (m *OS) stopSidebarSessions() {
	if m.sidebarSessionDone != nil {
		close(m.sidebarSessionDone)
		m.sidebarSessionDone = nil
	}
	for _, edge := range []sidebarEdge{sidebarLeft, sidebarRight} {
		m.closeSidebarSession(edge)
	}
}
