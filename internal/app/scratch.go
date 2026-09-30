package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The scratch terminal, after tmux-floax: one key shows a small layout of
// panes in a box over the current workspace, and the same key hides it again.
//
// A scratch group is a workspace of its own (see session/scratch_workspace.go):
// its panes are ordinary tiled windows on a workspace numbered from
// session.ScratchWorkspaceBase up. Showing the group makes that workspace the
// current one on this client, with the workspace the user left drawn under
// it (the backdrop) and the pane region cut down to the scratch box
// (scratchRegion). Hiding the group goes back. So everything that works on the
// current workspace works inside the scratch with no code of its own: split,
// focus movement, resize, zoom, close, multifocus, the BSP tree and its sync,
// layout save and the daemon's restore. And a hidden group is simply a
// workspace nobody is on, which every list, the dock, the renderer, tiling and
// focus already leave alone.
//
// Each group has its own workspace, so each [[keybindings.command]] entry of
// type scratch keeps its own layout. One group is on the screen at a time: a
// show from inside another group switches straight to the new one.
//
// The session's current workspace stays the one the user left: this client
// sends that one (BuildSessionState), and the focus on a scratch pane is what
// tells every client to show the group (syncScratchView).
//
// When the last pane of a group closes, the group ends and the view goes
// back. The next press starts a new group.
//
// The key reaches this client even while a scratch pane has the focus. A
// binding is looked up here before a key is passed to the pane.

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

// isScratch reports whether w is a pane of a scratch group. The mark is the
// daemon's, so a pane the user opened with the same name is not one.
func isScratch(w *terminal.Window) bool {
	return w != nil && w.IsScratch
}

// scratchIndex is the index of a pane of the built-in scratch group, or -1.
func (m *OS) scratchIndex() int {
	return m.scratchIndexNamed(scratchName)
}

// scratchIndexNamed is the index of a pane of the group kept under name, or
// -1. It is the pane the group had the focus on, when the client knows it.
func (m *OS) scratchIndexNamed(name string) int {
	m.forgetGoneDeadScratch()
	first := -1
	for i, w := range m.Windows {
		if !isScratch(w) || scratchNameOf(w) != name || m.deadScratch[w.ID] {
			continue
		}
		if first < 0 {
			first = i
		}
		if f, ok := m.WorkspaceFocus[w.Workspace]; ok && f == i {
			return i
		}
	}
	return first
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

// InScratchView reports whether this client shows a scratch group now.
func (m *OS) InScratchView() bool {
	return session.IsScratchWorkspace(m.CurrentWorkspace)
}

// ShownScratch is the index of the focused scratch pane while a group is on
// the screen, or -1.
func (m *OS) ShownScratch() int {
	if !m.InScratchView() || !m.hasFocusedWindow() || !isScratch(m.Windows[m.FocusedWindow]) {
		return -1
	}
	return m.FocusedWindow
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
	// index is a pane of the group, for show and hide.
	index int
	// refuse is the warning to show instead of acting.
	refuse string
}

// planScratch decides what the toggle does now.
//
// The group on the screen means hide. A group that exists means show it. None
// means create, unless a create is already on its way.
func (m *OS) planScratch(name string) scratchPlan {
	if i := m.scratchIndexNamed(name); i >= 0 {
		if m.Windows[i].Workspace == m.CurrentWorkspace {
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

// ToggleScratch shows the built-in scratch group, or hides it when it is on
// the screen. The first press creates it.
func (m *OS) ToggleScratch() tea.Cmd {
	return m.toggleScratch(m.builtinScratch())
}

// toggleScratch is ToggleScratch for any scratch: the built-in one or a
// command entry's.
func (m *OS) toggleScratch(spec scratchSpec) tea.Cmd {
	if !m.scratchWorkspaces() || m.legacyScratchIndex(spec.Name) >= 0 {
		return m.legacyScratchToggle(spec)
	}
	plan := m.planScratch(spec.Name)
	switch plan.action {
	case scratchRefuse:
		m.ShowNotification(plan.refuse, "warning", m.Settings.NotificationDuration)
	case scratchHide:
		m.leaveScratchView()
	case scratchShow:
		m.showScratch(plan.index)
	case scratchCreate:
		if !m.InScratchView() {
			m.rememberScratchReturn()
		}
		return m.createScratch(spec)
	}
	return nil
}

// rememberScratchReturn records the pane and the mode to go back to when the
// scratch group hides.
func (m *OS) rememberScratchReturn() {
	m.scratchReturnMode = m.Mode
	m.scratchReturnID = ""
	if w := m.GetFocusedWindow(); w != nil && !isScratch(w) {
		m.scratchReturnID = w.ID
	}
}

// showScratch shows the group of the pane at i, with the focus on that pane,
// in terminal mode. The switch into the group's workspace does the rest
// (enterScratchWorkspace).
func (m *OS) showScratch(i int) {
	w := m.Windows[i]
	if !m.InScratchView() {
		m.rememberScratchReturn()
	}
	m.scratchViewName = scratchNameOf(w)
	if w.Workspace != m.CurrentWorkspace {
		m.switchToWorkspace(w.Workspace, i)
	} else {
		m.FocusWindow(i)
	}
	if m.Mode != TerminalMode && m.hasFocusedWindow() {
		m.EnterTerminalMode()
	}
	m.MarkAllDirty()
}

// leaveScratchView goes back to the workspace the scratch group was shown
// over, to the pane that had the focus before, in the mode it was in.
func (m *OS) leaveScratchView() {
	if !m.InScratchView() {
		return
	}
	base := m.scratchBase
	if base < 1 {
		base = 1
	}
	back := -1
	for j, o := range m.Windows {
		if m.scratchReturnID != "" && o.ID == m.scratchReturnID && o.Workspace == base && !o.Minimized {
			back = j
			break
		}
	}
	m.switchToWorkspace(base, back)
	if !m.hasFocusedWindow() {
		if m.Mode == TerminalMode {
			m.ExitTerminalMode()
		}
	} else if m.scratchReturnMode != TerminalMode && m.Mode == TerminalMode {
		m.ExitTerminalMode()
	}
	m.scratchReturnID = ""
	m.MarkAllDirty()
}

// enterScratchWorkspace is the bookkeeping of a switch from an ordinary
// workspace into a scratch one, run by switchToWorkspaceHeld before it
// retiles: the workspace left is the backdrop, and the group always tiles.
func (m *OS) enterScratchWorkspace(from, to int) {
	if !session.IsScratchWorkspace(from) {
		m.scratchBase = from
		m.scratchBaseTiling = m.AutoTiling
	}
	m.AutoTiling = true
	if name, ok := m.scratchNameOnWorkspace(to); ok {
		m.scratchViewName = name
	}
}

// leaveScratchWorkspace is the bookkeeping of a switch from a scratch
// workspace to an ordinary one: the tiling mode the user left comes back.
func (m *OS) leaveScratchWorkspace() {
	m.AutoTiling = m.scratchBaseTiling
	m.scratchBase = 0
	m.scratchViewName = ""
}

// scratchNameOnWorkspace is the name of the group on a scratch workspace.
func (m *OS) scratchNameOnWorkspace(ws int) (string, bool) {
	for _, w := range m.Windows {
		if w.Workspace == ws && isScratch(w) {
			return scratchNameOf(w), true
		}
	}
	return "", false
}

// scratchWorkspaceFree is the lowest scratch workspace no window is on, for a
// group made without a daemon.
func (m *OS) scratchWorkspaceFree() int {
	used := map[int]bool{}
	for _, w := range m.Windows {
		used[w.Workspace] = true
	}
	ws := session.ScratchWorkspaceBase
	for used[ws] {
		ws++
	}
	return ws
}

// leaveEmptyScratchView goes back when the group on the screen has no pane
// left: its last pane closed, and the group ended.
func (m *OS) leaveEmptyScratchView() {
	if !m.InScratchView() || m.scratchPending != "" {
		return
	}
	for _, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace {
			return
		}
	}
	m.leaveScratchView()
}

// syncScratchView shows or leaves a group to match the focus a state push
// set. The focus is session state and the scratch view is not, so a client
// that focuses a scratch pane makes every client of the session show its
// group, and a focus back on an ordinary pane takes every client out.
func (m *OS) syncScratchView() {
	if !m.hasFocusedWindow() || !m.scratchWorkspaces() {
		return
	}
	w := m.Windows[m.FocusedWindow]
	switch {
	case isScratch(w) && w.Workspace != m.CurrentWorkspace:
		m.showScratch(m.FocusedWindow)
	case !isScratch(w) && m.InScratchView() && w.Workspace != m.CurrentWorkspace:
		m.leaveScratchView()
	}
}

// scratchRegion is the scratch box while a group is on the screen: the outer
// box, which the frame is drawn on, and the inner one, which the group's panes
// tile. The size is the group's spec, read from the config on every call, in
// the pane region the session agreed.
func (m *OS) scratchRegion() (outer, inner layout.Rect, ok bool) {
	if !m.InScratchView() {
		return layout.Rect{}, layout.Rect{}, false
	}
	spec, known := m.scratchSpecFor(m.scratchViewName)
	if !known {
		spec = scratchSpec{Width: config.ScratchDefaultWidth, Height: config.ScratchDefaultHeight}
	}
	left, top := m.GetLeftMargin(), m.GetTopMargin()
	cw, ch := m.GetContentWidth(), m.GetUsableHeight()
	w := session.ResolvePopupSize(spec.Width, session.PopupDefaultWidth, cw, session.PopupMinWidth+2)
	h := session.ResolvePopupSize(spec.Height, session.PopupDefaultHeight, ch, session.PopupMinHeight+2)
	outer = layout.Rect{X: left + (cw-w)/2, Y: top + (ch-h)/2, W: w, H: h}
	inner = layout.Rect{X: outer.X + 1, Y: outer.Y + 1, W: max(outer.W-2, 1), H: max(outer.H-2, 1)}
	return outer, inner, true
}

// PaneLeft, PaneTop, PaneWidth and PaneHeight are the region the panes of the
// current workspace are laid out in: the content region, or the inside of the
// scratch box while a group is on the screen. Tiling, zoom, popups and the
// border drag read these. Chrome (the dock, the sidebar, overlays) reads the
// Get*Margin family, which never moves.
func (m *OS) PaneLeft() int {
	if _, in, ok := m.scratchRegion(); ok {
		return in.X
	}
	return m.GetLeftMargin()
}

// PaneTop is PaneLeft's top edge.
func (m *OS) PaneTop() int {
	if _, in, ok := m.scratchRegion(); ok {
		return in.Y
	}
	return m.GetTopMargin()
}

// PaneWidth is the width of the pane region.
func (m *OS) PaneWidth() int {
	if _, in, ok := m.scratchRegion(); ok {
		return in.W
	}
	return m.GetContentWidth()
}

// PaneHeight is the height of the pane region.
func (m *OS) PaneHeight() int {
	if _, in, ok := m.scratchRegion(); ok {
		return in.H
	}
	return m.GetUsableHeight()
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

// createLocalScratch makes a scratch group in a session without a daemon: a
// free scratch workspace, shown, with one pane.
func (m *OS) createLocalScratch(dir string, spec scratchSpec) {
	ws := m.scratchWorkspaceFree()
	m.scratchViewName = spec.Name
	m.switchToWorkspace(ws, -1)
	before := len(m.Windows)
	m.AddWindowIn(dir, "", spec.Command...)
	if len(m.Windows) == before {
		m.leaveScratchView()
		return
	}
	w := m.Windows[len(m.Windows)-1]
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
		"session": req.Session,
		// The box's frame names the group, so its first pane takes the
		// title its shell sets.
		"name":         "",
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
	// also what enters terminal mode.
	if isLegacyScratch(m.Windows[i]) {
		m.legacyShowScratch(i)
		return
	}
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

// HideShownScratch hides the scratch group when it is on the screen, and
// gives the focus back as the key does. It reports whether it hid one.
func (m *OS) HideShownScratch() bool {
	if i := m.shownLegacyScratch(); i >= 0 {
		m.legacyHideScratch(i)
		return true
	}
	if !m.InScratchView() {
		return false
	}
	m.leaveScratchView()
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
	label := scratchNameOf(w)
	if spec, ok := m.scratchSpecFor(label); ok && spec.Title != "" {
		label = spec.Title
	}
	m.ShowNotification(fmt.Sprintf("The command %s stopped at once.", label), "error", m.Settings.NotificationDuration)
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
	m.leaveEmptyScratchView()
}

// scratchBackdropZ moves every layer of the workspace a scratch group is
// shown over below the group's frame and panes.
const scratchBackdropZ = -10000

// renderScratchFrame is the scratch box while a group is on the screen: a
// border with the group's name, filled, between the backdrop and the group's
// panes. The fill keeps the backdrop out of the gaps between the panes.
func (m *OS) renderScratchFrame() *lipgloss.Layer {
	outer, _, ok := m.scratchRegion()
	if !ok || outer.W < 2 || outer.H < 2 {
		return nil
	}
	title := m.scratchViewName
	if spec, known := m.scratchSpecFor(m.scratchViewName); known && spec.Title != "" {
		title = spec.Title
	}
	inner := outer.W - 2
	label := ""
	if title != "" && inner >= 6 {
		label = " " + truncateRunes(title, inner-4) + " "
	}
	top := "╭─" + label + strings.Repeat("─", max(inner-1-lipgloss.Width(label), 0)) + "╮"
	rows := make([]string, 0, outer.H)
	rows = append(rows, top)
	mid := "│" + strings.Repeat(" ", inner) + "│"
	for range outer.H - 2 {
		rows = append(rows, mid)
	}
	rows = append(rows, "╰"+strings.Repeat("─", inner)+"╯")
	style := lipgloss.NewStyle().Foreground(theme.BorderFocusedWindowOn(m.host.bg))
	return lipgloss.NewLayer(style.Render(strings.Join(rows, "\n"))).X(outer.X).Y(outer.Y).Z(-1).ID("scratch-frame")
}

// truncateRunes cuts s to at most n runes.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:max(n, 0)])
}

// ScratchBox is the scratch box while a group is on the screen.
func (m *OS) ScratchBox() (layout.Rect, bool) {
	outer, _, ok := m.scratchRegion()
	return outer, ok
}

// dockWorkspace is the workspace the dock names: the one a scratch group is
// shown over, while a group is on the screen. The group's own number means
// nothing to a person.
func (m *OS) dockWorkspace() int {
	if m.InScratchView() {
		return max(m.scratchBase, 1)
	}
	return m.CurrentWorkspace
}
