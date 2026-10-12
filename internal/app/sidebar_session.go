package app

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
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
	zoomPTY    string // client-local expansion of a rail pane, not the daemon layout
	workspace  int
	paneIndex  int
	width      int
	height     int
	problem    string
	failures   int // consecutive failed attachments, for bounded reconnect backoff
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

// resetFailuresWhenReady leaves post-attach snapshot and subscription failures
// in the same backoff sequence as connect failures. An empty session is ready
// immediately; otherwise every visible pane must be subscribed first.
func (v *sidebarSessionView) resetFailuresWhenReady() {
	if len(v.pending) == 0 {
		v.failures = 0
	}
}

// A failed attachment backs off; the generation invalidates obsolete retries.
func (m *OS) retrySidebarSession(edge sidebarEdge) tea.Cmd {
	v := &m.sidebarSessions[edge]
	generation := v.generation
	delay := min(2*time.Second*time.Duration(1<<min(v.failures, 4)), 30*time.Second)
	v.failures = min(v.failures+1, 4)
	return tea.Tick(delay, func(time.Time) tea.Msg {
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
				v.failures = 0
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
			m.MarkAllDirty()
		}
	}
	return tea.Batch(cmds...)
}

func (m *OS) closeSidebarSession(edge sidebarEdge) {
	v := &m.sidebarSessions[edge]
	v.generation++ // invalidate any connect/snapshot still in flight
	if v.client != nil {
		_ = v.client.Close() // detaches only this viewer, not the daemon session
		v.client = nil
	}
	for _, w := range v.windows {
		m.recordWindowButtons(w.ID, nil)
		w.Close()
	}
	v.window, v.windows, v.pending, v.panes = nil, nil, nil, nil
	v.name, v.ptyID, v.focusPTY, v.zoomPTY, v.problem = "", "", "", "", ""
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

func (m *OS) listenSidebarSession() tea.Cmd {
	// Capture the shutdown channel before starting the async command. Stop
	// closes it and clears the OS field; reading that field later could race
	// with shutdown or leave a listener parked on a nil channel forever.
	events, states, done := m.sidebarSessionEvents, m.sidebarSessionStateEvents, m.sidebarSessionDone
	return func() tea.Msg {
		select {
		case event := <-events:
			return sidebarSessionNotice{event}
		case event := <-states[sidebarLeft]:
			return sidebarSessionNotice{event}
		case event := <-states[sidebarRight]:
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
	if w := v.windows[v.zoomPTY]; v.zoomPTY != "" && w != nil {
		w.X, w.Y = sidebarEdgeX(edge, v.width, m.GetRenderWidth()), m.viewReserve().Top
		w.ResizeVisual(v.width, body)
		w.MarkContentDirty()
		return
	}
	y := m.viewReserve().Top
	for i, pane := range v.panes {
		h := body / count
		if i < body%count {
			h++
		}
		if w := v.windows[pane.PTYID]; w != nil {
			w.X, w.Y = sidebarEdgeX(edge, v.width, m.GetRenderWidth()), y
			w.ResizeVisual(v.width, h)
			w.MarkContentDirty()
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
	v.paneIndex = 0
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
	if v.zoomPTY != "" {
		zoomVisible := false
		for _, pane := range eligible {
			zoomVisible = zoomVisible || pane.PTYID == v.zoomPTY
		}
		if !zoomVisible {
			v.zoomPTY = ""
		}
	}
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
			m.recordWindowButtons(w.ID, nil)
			w.Close()
			delete(v.windows, id)
		}
	}
	for id := range v.pending {
		if !visible[id] {
			delete(v.pending, id)
		}
	}
	if v.zoomPTY != "" && !visible[v.zoomPTY] {
		v.zoomPTY = ""
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
			w.CustomName = pane.CustomName
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
			failures := v.failures
			m.closeSidebarSession(event.edge)
			cmd := m.refreshSidebarSessions()
			m.sidebarSessions[event.edge].failures = failures
			return cmd
		}
	case "connect":
		if event.err != nil {
			v.problem = event.err.Error()
			m.MarkAllDirty()
			return m.retrySidebarSession(event.edge)
		}
		v.client = event.client
		edge, generation := event.edge, v.generation
		m.WireSidebarSessionClient(v.client, edge, generation)
		v.client.StartReadLoop()
		cmd := m.applySidebarSessionState(edge, event.state)
		v.resetFailuresWhenReady() // an empty session needs no subscriptions
		return cmd
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
		w.CustomName = event.pane.CustomName
		w.ForegroundCmd = event.pane.ForegroundCmd
		w.DaemonWriteFunc = func(data []byte) error { return client.WritePTY(ptyID, data) }
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
			// Crop the subscribed guest to its rail frame. The shared daemon
			// PTY keeps the dimensions chosen by ordinary session clients.
			v.resetFailuresWhenReady()
			m.layoutSidebarSessionPanes(event.edge)
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
	// The rail has no extra header: its panes use the same title bars as
	// ordinary windows. Only an empty rail needs a loading placeholder.
	if len(v.panes) == 0 {
		lines[0] = sidebarFit(" Connecting to session…", width, m.railGround())
	}
	for _, pane := range v.panes {
		w := v.windows[pane.PTYID]
		if w == nil || (v.zoomPTY != "" && v.zoomPTY != pane.PTYID) {
			continue
		}
		focused := m.sidebarSessionFocus == int(edge)+1 && m.SidebarSessionWindow() == w
		border := theme.BorderUnfocusedOn(m.host.bg)
		if focused {
			border = theme.BorderFocusedTerminalOn(m.host.bg)
		}
		body := strings.Split(m.renderWindowBox(w, 0, focused, border), "\n")
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

// SidebarSessionHeaderAt claims the first rail row while its pane is loading.
// Once loaded, SidebarSessionTitlePaneAt resolves the ordinary title bar.
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
		v := &m.sidebarSessions[edge-1]
		for _, pane := range v.panes {
			w := v.windows[pane.PTYID]
			if w != nil && (v.zoomPTY == "" || v.zoomPTY == pane.PTYID) && row >= w.Y && row < w.Y+w.Height {
				return w
			}
		}
	}
	return nil
}

// SidebarSessionTitlePaneAt finds the ordinary title bar of a rail pane.
// Its first row is intentionally outside SidebarSessionAt's body hit.
func (m *OS) SidebarSessionTitlePaneAt(x, y int) (int, *terminal.Window) {
	x, y = m.ScreenPoint(x, y)
	for _, side := range []sidebarEdge{sidebarLeft, sidebarRight} {
		if !m.sidebarSessionVisible(side) {
			continue
		}
		v := &m.sidebarSessions[side]
		border := sidebarEdgeX(side, v.width, m.GetRenderWidth())
		if side == sidebarLeft {
			border += v.width - 1
		}
		for _, pane := range v.panes {
			w := v.windows[pane.PTYID]
			if w != nil && (v.zoomPTY == "" || v.zoomPTY == pane.PTYID) && y == w.Y && x >= w.X && x < w.X+w.Width && x != border {
				return int(side) + 1, w
			}
		}
	}
	return 0, nil
}

// sidebarSessionWindowByID finds a currently drawn rail pane for title-bar
// button hit testing. A zoomed rail does not expose the hidden panes' buttons.
func (m *OS) sidebarSessionWindowByID(id string) *terminal.Window {
	for _, side := range []sidebarEdge{sidebarLeft, sidebarRight} {
		if !m.sidebarSessionVisible(side) {
			continue
		}
		v := &m.sidebarSessions[side]
		for _, pane := range v.panes {
			if pane.ID == id && (v.zoomPTY == "" || v.zoomPTY == pane.PTYID) {
				return v.windows[pane.PTYID]
			}
		}
	}
	return nil
}

// ToggleSidebarSessionZoom expands this pane within its own rail, leaving
// the daemon session's ordinary pane set and the center session unchanged.
func (m *OS) ToggleSidebarSessionZoom(edge int, w *terminal.Window) {
	if edge < 1 || edge > len(m.sidebarSessions) || w == nil {
		return
	}
	v := &m.sidebarSessions[edge-1]
	if v.zoomPTY == w.PTYID {
		v.zoomPTY = ""
	} else {
		v.zoomPTY = w.PTYID
	}
	for _, pane := range v.panes {
		if v.zoomPTY != "" && pane.PTYID != v.zoomPTY {
			m.recordWindowButtons(pane.ID, nil)
		}
	}
	m.layoutSidebarSessionPanes(sidebarEdge(edge - 1))
	m.MarkAllDirty()
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
