package app

import (
	"encoding/json"
	"errors"
	"fmt"
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

// scratchName is the name the built-in scratch terminal carries on its border
// and in session state.
const scratchName = config.DefaultScratchName

// scratchSpec is one scratch terminal: the built-in one, or a
// [[keybindings.command]] entry of type scratch. Each has its own pane, found
// by Name.
type scratchSpec struct {
	// Name keys the pane in session state (WindowState.ScratchName).
	Name string
	// Title is what the popup border shows.
	Title string
	// Command is the argv the pane runs. Empty runs the user's shell.
	Command []string
	// Width and Height are the popup's size, as tuios popup takes them.
	Width, Height string
}

// builtinScratch is the scratch terminal toggle_scratch shows: the user's
// shell, sized by [scratch].
func (m *OS) builtinScratch() scratchSpec {
	cfg := m.scratchConfig()
	return scratchSpec{Name: scratchName, Title: scratchName, Width: cfg.WidthSpec(), Height: cfg.HeightSpec()}
}

// scratchSpecFor is the spec behind a scratch pane's name, for a show that
// did not come from its key (a focus jump). ok is false for a name the config
// no longer has.
func (m *OS) scratchSpecFor(name string) (scratchSpec, bool) {
	if name == "" || name == scratchName {
		return m.builtinScratch(), true
	}
	if m.UserConfig == nil {
		return scratchSpec{}, false
	}
	c, ok := m.UserConfig.Keybindings.CommandFor(config.CommandActionPrefix + name)
	if !ok || c.ResolvedType() != config.CommandTypeScratch {
		return scratchSpec{}, false
	}
	return m.commandScratchSpec(c, nil), true
}

// scratchNameOf is the name a scratch pane is kept under. A pane from before
// scratch names is the built-in one.
func scratchNameOf(w *terminal.Window) string {
	if w.ScratchName == "" {
		return scratchName
	}
	return w.ScratchName
}

// scratchPendingMax is the backstop on a create that is on its way. A create
// is on its way from the press until the daemon refuses it or the pane
// arrives, and a second press in that time does nothing, so a double press
// cannot ask for two panes. The backstop only matters when the daemon never
// answers.
const scratchPendingMax = 10 * time.Second

// ScratchOpenedMsg reports the outcome of the call that creates the scratch
// terminal in a daemon session.
type ScratchOpenedMsg struct {
	// Label names the scratch in a message.
	Label string
	Err   error
}

// scratchStoppedWait is how long the create waits to see whether the command
// exits at once, as a command that is not installed does.
const scratchStoppedWait = 1500 * time.Millisecond

// ScratchStoppedError is a scratch command that exited within
// scratchStoppedWait of its start.
type ScratchStoppedError struct {
	Code int
	// WindowID is the pane that stopped. The client may still hold it until
	// the push that removes it arrives. See deadScratch.
	WindowID string
}

func (e ScratchStoppedError) Error() string {
	return fmt.Sprintf("stopped with exit code %d", e.Code)
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

// scratchIndex is the index of the built-in scratch terminal, or -1.
func (m *OS) scratchIndex() int {
	return m.scratchIndexNamed(scratchName)
}

// scratchIndexNamed is the index of the scratch pane kept under name, or -1.
func (m *OS) scratchIndexNamed(name string) int {
	m.forgetGoneDeadScratch()
	for i, w := range m.Windows {
		if isScratch(w) && scratchNameOf(w) == name && !m.deadScratch[w.ID] {
			return i
		}
	}
	return -1
}

// forgetGoneDeadScratch drops the dead panes the client no longer holds.
func (m *OS) forgetGoneDeadScratch() {
	if len(m.deadScratch) == 0 {
		return
	}
	held := make(map[string]bool, len(m.Windows))
	for _, w := range m.Windows {
		held[w.ID] = true
	}
	for id := range m.deadScratch {
		if !held[id] {
			delete(m.deadScratch, id)
		}
	}
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
func (m *OS) planScratch(name string) scratchPlan {
	if i := m.scratchIndexNamed(name); i >= 0 {
		w := m.Windows[i]
		if !w.Minimized && w.Workspace == m.CurrentWorkspace {
			return scratchPlan{action: scratchHide, index: i}
		}
		return scratchPlan{action: scratchShow, index: i}
	}
	if m.scratchPending == name && time.Since(m.scratchPendingAt) < scratchPendingMax {
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
	return m.toggleScratch(m.builtinScratch())
}

// toggleScratch is ToggleScratch for any scratch: the built-in one or a
// command entry's.
func (m *OS) toggleScratch(spec scratchSpec) tea.Cmd {
	plan := m.planScratch(spec.Name)
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
		return m.createScratch(spec)
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
	// The config is read on each show, so a size set since the last show
	// applies now.
	if spec, ok := m.scratchSpecFor(scratchNameOf(w)); ok && m.UserConfig != nil {
		w.PopupWidth, w.PopupHeight = spec.Width, spec.Height
	}
	// One scratch terminal is on the screen at a time.
	for j, o := range m.Windows {
		if j != i && isScratch(o) && !o.Minimized {
			o.Minimized = true
			o.InvalidateCache()
		}
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
func (m *OS) createScratch(spec scratchSpec) tea.Cmd {
	dir := m.scratchDir()
	if !m.IsDaemonSession || m.DaemonClient == nil {
		m.createLocalScratch(dir, spec)
		return nil
	}
	m.scratchPending = spec.Name
	m.scratchPendingAt = time.Now()
	req := scratchRequest{
		Session:   m.SessionName,
		Name:      spec.Name,
		Title:     spec.Title,
		Command:   spec.Command,
		Dir:       dir,
		Width:     spec.Width,
		Height:    spec.Height,
		Workspace: m.CurrentWorkspace,
	}
	label := spec.Title
	return func() tea.Msg {
		return ScratchOpenedMsg{Label: label, Err: scratchOpener(req)}
	}
}

// createLocalScratch makes the scratch terminal in a session without a
// daemon. The shell starts at the popup's size, so its first prompt is drawn
// for the box it is in.
func (m *OS) createLocalScratch(dir string, spec scratchSpec) {
	w := m.newLocalPopup(dir, spec.Title, spec.Width, spec.Height, spec.Command)
	if w == nil {
		return
	}
	w.IsScratch = true
	w.ScratchName = spec.Name
	if m.scratchStarted == nil {
		m.scratchStarted = map[string]time.Time{}
	}
	m.scratchStarted[w.ID] = time.Now()
	m.showScratch(len(m.Windows) - 1)
}

// newLocalPopup makes a popup pane in a session without a daemon and appends
// it. The command starts at the popup's size, so its first frame is drawn for
// the box it is in. An empty command runs the user's shell.
func (m *OS) newLocalPopup(dir, title, widthSpec, heightSpec string, command []string) *terminal.Window {
	probe := &terminal.Window{PopupWidth: widthSpec, PopupHeight: heightSpec}
	x, y, width, height := m.popupRect(probe)

	id := createID()
	w, err := terminal.NewWindowIn(dir, id, title, x, y, width, height, len(m.Windows),
		m.WindowExitChan, m.PTYDataChan, m.Settings.ScrollbackLines, command...)
	if err != nil {
		m.LogError("Failed to create the popup %s: %v", title, err)
		m.ShowNotification("The popup did not open: "+err.Error(), "error", m.Settings.NotificationDuration)
		return nil
	}
	if caps := m.hostCaps(); caps.CellWidth > 0 && caps.CellHeight > 0 {
		w.SetCellPixelDimensions(caps.CellWidth, caps.CellHeight)
	}
	w.Workspace = m.CurrentWorkspace
	w.CustomName = title
	w.IsPopup = true
	w.IsFloating = true
	w.PopupWidth = probe.PopupWidth
	w.PopupHeight = probe.PopupHeight

	m.installPassthroughs(w)
	m.setupCwdWatch(w)
	m.Windows = append(m.Windows, w)
	return w
}

// scratchRequest is what the daemon call needs, copied off the model so the
// call can run off the update goroutine.
type scratchRequest struct {
	Session       string
	Name, Title   string
	Command       []string
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
		"session":      req.Session,
		"name":         req.Title,
		"width":        req.Width,
		"height":       req.Height,
		"workspace":    req.Workspace,
		"scratch":      true,
		"scratch_name": req.Name,
	}
	if len(req.Command) > 0 {
		params["command"] = req.Command
	}
	if req.Dir != "" {
		params["cwd"] = req.Dir
	}
	// Wait a moment for the command to exit. One that exits at once (a typo,
	// a program that is not installed) is reported with its code, and the
	// create ends. The daemon keeps the popup when the wait runs out.
	params["wait"] = true
	params["timeout"] = int(scratchStoppedWait / time.Millisecond)
	raw, err := c.CallWithTimeout("popup", params, scratchStoppedWait+5*time.Second)
	if call, ok := errors.AsType[*session.VerbCallError](err); ok && call.Code == session.ErrVerbTimeout {
		return nil
	}
	if err != nil {
		return err
	}
	var res struct {
		Type     string `json:"type"`
		ExitCode int    `json:"exit_code"`
		WindowID string `json:"window_id"`
	}
	if json.Unmarshal(raw, &res) == nil && res.Type == "popup_result" {
		return ScratchStoppedError{Code: res.ExitCode, WindowID: res.WindowID}
	}
	return nil
}

// handleScratchOpened reports a failed create and ends it. A create the
// daemon accepted stays on its way until its pane arrives in a state push,
// where maybeFocusScratch takes it: until then this client does not hold the
// pane, and a second press would ask for another.
func (m *OS) handleScratchOpened(msg ScratchOpenedMsg) {
	if msg.Err == nil {
		return
	}
	m.scratchPending = ""
	if stopped, ok := errors.AsType[ScratchStoppedError](msg.Err); ok {
		// The daemon closed the pane, but the push that removes it can come
		// after this answer, on the client's own connection. Until then a
		// press must not find it: it would show or hide a dead pane and say
		// nothing, where the person must get the command or its report.
		if stopped.WindowID != "" {
			if m.deadScratch == nil {
				m.deadScratch = map[string]bool{}
			}
			m.deadScratch[stopped.WindowID] = true
		}
		m.ShowNotification(fmt.Sprintf("The command %s stopped with exit code %d.", msg.Label, stopped.Code), "error", m.Settings.NotificationDuration)
		return
	}
	m.ShowNotification("The scratch terminal did not open: "+msg.Err.Error(), "error", m.Settings.NotificationDuration)
}

// maybeFocusScratch ends the create on its way once its pane arrives, and
// shows it the way a press does, so the user can type into it at once.
func (m *OS) maybeFocusScratch() {
	if m.scratchPending == "" {
		return
	}
	if time.Since(m.scratchPendingAt) >= scratchPendingMax {
		m.scratchPending = ""
		return
	}
	i := m.scratchIndexNamed(m.scratchPending)
	if i < 0 {
		return
	}
	m.scratchPending = ""
	// Always, even when the daemon's push already focused it: the show is
	// also what hides another scratch pane that is on the screen.
	m.showScratch(i)
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
	for i, w := range m.Windows {
		if isScratch(w) && !w.Minimized && w.Workspace == m.CurrentWorkspace {
			return i
		}
	}
	return -1
}

// parkScratch hides a shown scratch terminal without moving the focus or the
// mode. It is for a focus that is already on its way to another pane (see
// FocusWindow). The press of the scratch key uses hideScratch, which also
// gives the focus back.
func (m *OS) parkScratch() {
	i := -1
	for j, w := range m.Windows {
		if isScratch(w) && !w.Minimized {
			i = j
			break
		}
	}
	if i < 0 {
		return
	}
	m.Windows[i].Minimized = true
	m.Windows[i].InvalidateCache()
	m.scratchReturnID = ""
	// The mode goes back to the one the show found, as a hide by the key
	// does. The caller may still change it for the pane it focuses.
	if m.scratchReturnMode != TerminalMode && m.Mode == TerminalMode {
		m.ExitTerminalMode()
	}
	m.MarkAllDirty()
	m.SyncStateToDaemon()
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

// noteLocalScratchExit reports a local scratch pane whose command exited
// within scratchStoppedWait of its start. The daemon path reports it from
// the popup call instead (openScratchPopup). The pane is not started again,
// so a command that cannot run does not loop.
func (m *OS) noteLocalScratchExit(w *terminal.Window) {
	started, ok := m.scratchStarted[w.ID]
	delete(m.scratchStarted, w.ID)
	if !ok || !isScratch(w) || time.Since(started) >= scratchStoppedWait {
		return
	}
	m.ShowNotification(fmt.Sprintf("The command %s stopped at once.", w.CustomName), "error", m.Settings.NotificationDuration)
}

// pruneOrphanScratches closes each scratch pane whose entry the config no
// longer has: an entry removed, renamed, or given a new description with no
// name, which changes its name. Hidden, such a pane had no key to show it and
// ran on. The built-in scratch terminal always has its key.
func (m *OS) pruneOrphanScratches() {
	for i := len(m.Windows) - 1; i >= 0; i-- {
		w := m.Windows[i]
		if !isScratch(w) || scratchNameOf(w) == scratchName {
			continue
		}
		if _, ok := m.scratchSpecFor(scratchNameOf(w)); ok {
			continue
		}
		label := w.CustomName
		if label == "" {
			label = scratchNameOf(w)
		}
		m.DeleteWindow(i)
		m.ShowNotification(fmt.Sprintf("The scratch popup %s closed. config.toml has no entry for it now.", label), "info", m.Settings.NotificationDuration)
	}
}
