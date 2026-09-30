package app

import (
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The scratch terminal, after tmux-floax: one key shows a shell in a popup
// over the current layout, and the same key hides it again.
//
// It is one pane per session, a popup marked as the scratch terminal
// (session.WindowState.Scratch). The first press creates it. After that the
// key never closes it: hiding minimizes it, so the shell keeps running with
// its scrollback, and the next press shows the same pane on whichever
// workspace the user is on, focused and in terminal mode.
//
// Minimizing is what keeps a hidden pane out of the layout, the renderer, the
// focus cycle and hit testing, because all of them already skip a minimized
// pane. What minimizing would add, a dock entry and a restore digit, is taken
// back out explicitly: every place that offers minimized panes to the user
// asks terminal.Window.HiddenScratch first. The pane stays in the session
// state, so it survives a detach, and the daemon brings it back hidden after
// a restart (see daemon_resurrect.go).
//
// When its shell exits the pane closes like any popup, and the next press
// starts a new one.
//
// The key reaches this client even while the popup has the focus. A binding
// is looked up here before a key is passed to the pane, so the shell never
// sees the toggle, and pressing it from inside the popup hides the popup.

// scratchName is the name the scratch terminal carries on its border.
const scratchName = "scratch"

// scratchPendingMax is the backstop on a create that is on its way. A create
// is on its way from the press until the daemon refuses it or the pane
// arrives, and a second press in that time does nothing, so a double press
// cannot ask for two panes. The backstop only matters when the daemon never
// answers.
const scratchPendingMax = 10 * time.Second

// ScratchOpenedMsg reports the outcome of the call that creates the scratch
// terminal in a daemon session.
type ScratchOpenedMsg struct {
	Err error
}

// scratchOpener creates the pane through the daemon. Tests replace it.
var scratchOpener = openScratchPopup

// scratchConfig is the [scratch] table in force.
func (m *OS) scratchConfig() config.ScratchConfig {
	if m.UserConfig == nil {
		return config.ScratchConfig{}
	}
	return m.UserConfig.Scratch
}

// isScratch reports whether w is the scratch terminal. The mark is the
// daemon's, so a popup the user opened with the same name is not one.
func isScratch(w *terminal.Window) bool {
	return w != nil && w.IsPopup && w.IsScratch
}

// scratchIndex is the index of the scratch terminal, or -1.
func (m *OS) scratchIndex() int {
	for i, w := range m.Windows {
		if isScratch(w) {
			return i
		}
	}
	return -1
}

// scratchAction is what one press of the toggle does.
type scratchAction int

const (
	scratchNothing scratchAction = iota
	scratchCreate
	scratchShow
	scratchHide
	scratchRefuse
)

// scratchPlan is what one press of the toggle does. It is worked out apart
// from doing it, so a test can read the decision without a daemon.
type scratchPlan struct {
	action scratchAction
	// index is the scratch terminal, for show and hide.
	index int
	// refuse is the warning to show instead of acting.
	refuse string
}

// planScratch decides what the toggle does now.
//
// A scratch terminal on the screen means hide. One that is hidden, or left
// on another workspace, means show here. None means create, unless a create
// is already on its way.
func (m *OS) planScratch() scratchPlan {
	if i := m.scratchIndex(); i >= 0 {
		w := m.Windows[i]
		if !w.Minimized && w.Workspace == m.CurrentWorkspace {
			return scratchPlan{action: scratchHide, index: i}
		}
		return scratchPlan{action: scratchShow, index: i}
	}
	if m.scratchPending && time.Since(m.scratchPendingAt) < scratchPendingMax {
		return scratchPlan{action: scratchNothing, index: -1}
	}
	if m.IsDaemonSession && m.DaemonClient != nil && m.AttachedHost != "" {
		// The create goes to the daemon on this machine, and the session is
		// on another one.
		return scratchPlan{action: scratchRefuse, index: -1,
			refuse: "The scratch terminal works only in a session on this machine."}
	}
	return scratchPlan{action: scratchCreate, index: -1}
}

// ToggleScratch shows the scratch terminal on the current workspace, or hides
// it when it is already there. The first press creates it.
func (m *OS) ToggleScratch() tea.Cmd {
	plan := m.planScratch()
	switch plan.action {
	case scratchRefuse:
		m.ShowNotification(plan.refuse, "warning", m.Settings.NotificationDuration)
	case scratchHide:
		m.hideScratch(plan.index)
	case scratchShow:
		m.rememberScratchReturn()
		m.showScratch(plan.index)
	case scratchCreate:
		m.rememberScratchReturn()
		return m.createScratch()
	}
	return nil
}

// rememberScratchReturn records the pane and the mode to go back to when the
// scratch terminal hides.
func (m *OS) rememberScratchReturn() {
	m.scratchReturnMode = m.Mode
	m.scratchReturnID = ""
	if w := m.GetFocusedWindow(); w != nil && !isScratch(w) {
		m.scratchReturnID = w.ID
	}
}

// showScratch puts the scratch terminal on the current workspace, on top, and
// gives it the keyboard.
func (m *OS) showScratch(i int) {
	w := m.Windows[i]
	w.Workspace = m.CurrentWorkspace
	w.Minimized = false
	// [scratch] is read on each show, so a size set since the last show
	// applies now.
	if m.UserConfig != nil {
		cfg := m.scratchConfig()
		w.PopupWidth, w.PopupHeight = cfg.WidthSpec(), cfg.HeightSpec()
	}
	m.applyPopupRect(w, false)
	w.InvalidateCache()
	m.FocusWindow(i)
	if m.Mode != TerminalMode {
		m.EnterTerminalMode()
	}
	m.MarkAllDirty()
	m.SyncStateToDaemon()
}

// hideScratch minimizes the scratch terminal and gives the focus back to the
// pane that had it before the show, in the mode it was in.
func (m *OS) hideScratch(i int) {
	w := m.Windows[i]
	w.Minimized = true
	w.InvalidateCache()

	back := -1
	for j, o := range m.Windows {
		if o.ID == m.scratchReturnID && m.scratchReturnID != "" &&
			o.Workspace == m.CurrentWorkspace && !o.Minimized {
			back = j
			break
		}
	}
	if back >= 0 {
		m.FocusWindow(back)
	} else {
		m.FocusNextVisibleWindow()
	}
	switch {
	case !m.hasFocusedWindow() || isScratch(m.GetFocusedWindow()):
		m.FocusedWindow = -1
		if m.Mode == TerminalMode {
			m.ExitTerminalMode()
		}
	case m.scratchReturnMode != TerminalMode && m.Mode == TerminalMode:
		m.ExitTerminalMode()
	}
	m.scratchReturnID = ""
	m.MarkAllDirty()
	m.SyncStateToDaemon()
}

// scratchDir is the folder the scratch shell starts in: the focused pane's,
// when it is a folder on this machine, else the home folder.
func (m *OS) scratchDir() string {
	if w := m.GetFocusedWindow(); w != nil && w.Host == "" {
		dir := ""
		if w.Cwd != "" {
			dir, _ = localCwdPath(w.Cwd)
		} else {
			dir = w.CWD()
		}
		if info, err := os.Stat(dir); dir != "" && err == nil && info.IsDir() {
			return dir
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// createScratch makes the scratch terminal: through the daemon in a daemon
// session, here otherwise.
func (m *OS) createScratch() tea.Cmd {
	cfg := m.scratchConfig()
	dir := m.scratchDir()
	if !m.IsDaemonSession || m.DaemonClient == nil {
		m.createLocalScratch(dir, cfg)
		return nil
	}
	m.scratchPending = true
	m.scratchPendingAt = time.Now()
	req := scratchRequest{
		Session:   m.SessionName,
		Dir:       dir,
		Width:     cfg.WidthSpec(),
		Height:    cfg.HeightSpec(),
		Workspace: m.CurrentWorkspace,
	}
	return func() tea.Msg {
		return ScratchOpenedMsg{Err: scratchOpener(req)}
	}
}

// createLocalScratch makes the scratch terminal in a session without a
// daemon. The shell starts at the popup's size, so its first prompt is drawn
// for the box it is in.
func (m *OS) createLocalScratch(dir string, cfg config.ScratchConfig) {
	probe := &terminal.Window{PopupWidth: cfg.WidthSpec(), PopupHeight: cfg.HeightSpec()}
	x, y, width, height := m.popupRect(probe)

	id := createID()
	w, err := terminal.NewWindowIn(dir, id, scratchName, x, y, width, height, len(m.Windows),
		m.WindowExitChan, m.PTYDataChan, m.Settings.ScrollbackLines)
	if err != nil {
		m.LogError("Failed to create the scratch terminal: %v", err)
		m.ShowNotification("The scratch terminal did not open: "+err.Error(), "error", m.Settings.NotificationDuration)
		return
	}
	if caps := m.hostCaps(); caps.CellWidth > 0 && caps.CellHeight > 0 {
		w.SetCellPixelDimensions(caps.CellWidth, caps.CellHeight)
	}
	w.Workspace = m.CurrentWorkspace
	w.CustomName = scratchName
	w.IsPopup = true
	w.IsScratch = true
	w.IsFloating = true
	w.PopupWidth = probe.PopupWidth
	w.PopupHeight = probe.PopupHeight

	m.installPassthroughs(w)
	m.setupCwdWatch(w)
	m.Windows = append(m.Windows, w)
	m.showScratch(len(m.Windows) - 1)
}

// scratchRequest is what the daemon call needs, copied off the model so the
// call can run off the update goroutine.
type scratchRequest struct {
	Session       string
	Dir           string
	Width, Height string
	Workspace     int
}

// openScratchPopup asks the daemon for the scratch terminal. It runs as a
// command, never from Update, for the reason labelVerbCmd gives.
func openScratchPopup(req scratchRequest) error {
	c, err := session.DialVerbClient()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	params := map[string]any{
		"session":   req.Session,
		"name":      scratchName,
		"width":     req.Width,
		"height":    req.Height,
		"workspace": req.Workspace,
		"scratch":   true,
	}
	if req.Dir != "" {
		params["cwd"] = req.Dir
	}
	_, err = c.Call("popup", params)
	return err
}

// handleScratchOpened reports a failed create and ends it. A create the
// daemon accepted stays on its way until its pane arrives in a state push,
// where maybeFocusScratch takes it: until then this client does not hold the
// pane, and a second press would ask for another.
func (m *OS) handleScratchOpened(msg ScratchOpenedMsg) {
	if msg.Err == nil {
		return
	}
	m.scratchPending = false
	m.ShowNotification("The scratch terminal did not open: "+msg.Err.Error(), "error", m.Settings.NotificationDuration)
}

// maybeFocusScratch ends the create on its way once its pane arrives, and
// shows it the way a press does, so the user can type into it at once.
func (m *OS) maybeFocusScratch() {
	if !m.scratchPending {
		return
	}
	if time.Since(m.scratchPendingAt) >= scratchPendingMax {
		m.scratchPending = false
		return
	}
	i := m.scratchIndex()
	if i < 0 {
		return
	}
	m.scratchPending = false
	if w := m.Windows[i]; w.Minimized || w.Workspace != m.CurrentWorkspace || m.FocusedWindow != i || m.Mode != TerminalMode {
		m.showScratch(i)
	}
}

// windowCountForNotice is the window count the created and closed notices
// report. The scratch terminal is not counted: its show and hide are not a
// window made or closed.
func (m *OS) windowCountForNotice() int {
	n := 0
	for _, w := range m.Windows {
		if !isScratch(w) {
			n++
		}
	}
	return n
}

// ShownScratch is the index of the scratch terminal while it is on the screen
// (shown, on the current workspace), or -1.
func (m *OS) ShownScratch() int {
	i := m.scratchIndex()
	if i < 0 {
		return -1
	}
	if w := m.Windows[i]; w.Minimized || w.Workspace != m.CurrentWorkspace {
		return -1
	}
	return i
}

// parkScratch hides a shown scratch terminal without moving the focus or the
// mode. It is for a focus that is already on its way to another pane (see
// FocusWindow). The press of the scratch key uses hideScratch, which also
// gives the focus back.
func (m *OS) parkScratch() {
	i := m.scratchIndex()
	if i < 0 || m.Windows[i].Minimized {
		return
	}
	m.Windows[i].Minimized = true
	m.Windows[i].InvalidateCache()
	m.scratchReturnID = ""
	m.MarkAllDirty()
}

// HideShownScratch hides the scratch terminal when it is on the screen, and
// gives the focus back as the key does. It reports whether it hid one.
func (m *OS) HideShownScratch() bool {
	i := m.ShownScratch()
	if i < 0 {
		return false
	}
	m.hideScratch(i)
	return true
}
