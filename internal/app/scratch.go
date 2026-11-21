package app

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

const scratchName = config.DefaultScratchName

type scratchSpec struct {
	Name          string
	Title         string
	Command       []string
	Width, Height string
}

func (m *OS) builtinScratch() scratchSpec {
	cfg := m.scratchConfig()
	return scratchSpec{Name: scratchName, Title: scratchName, Width: cfg.WidthSpec(), Height: cfg.HeightSpec()}
}

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

func scratchNameOf(w *terminal.Window) string {
	if w.ScratchName == "" {
		return scratchName
	}
	return w.ScratchName
}

const scratchPendingMax = 10 * time.Second

const scratchPendingHostMax = 45 * time.Second

func (m *OS) scratchPendingLimit() time.Duration {
	if m.AttachedHost != "" {
		return scratchPendingHostMax
	}
	return scratchPendingMax
}

// ScratchOpenedMsg reports the outcome of the call that creates the scratch
// terminal in a daemon session.
type ScratchOpenedMsg struct {
	// Label names the scratch in a message.
	Label string
	// Name is the scratch group the create was for.
	Name string
	Err  error
}

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

var scratchOpener = openScratchPopup

func (m *OS) scratchConfig() config.ScratchConfig {
	if m.UserConfig == nil {
		return config.ScratchConfig{}
	}
	return m.UserConfig.Scratch
}

func isScratch(w *terminal.Window) bool {
	return w != nil && w.IsScratch
}

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

type scratchAction int

const (
	scratchNothing scratchAction = iota
	scratchCreate
	scratchShow
	scratchHide
)

type scratchPlan struct {
	action scratchAction
	index  int
}

func (m *OS) planScratch(name string) scratchPlan {
	if i := m.scratchIndexNamed(name); i >= 0 {
		if m.Windows[i].Workspace == m.CurrentWorkspace {
			return scratchPlan{action: scratchHide, index: i}
		}
		return scratchPlan{action: scratchShow, index: i}
	}
	if m.scratchPending == name && time.Since(m.scratchPendingAt) < m.scratchPendingLimit() {
		return scratchPlan{action: scratchNothing, index: -1}
	}
	return scratchPlan{action: scratchCreate, index: -1}
}

// ToggleScratch shows the built-in scratch group, or hides it when it is on
// the screen. The first press creates it.
func (m *OS) ToggleScratch() tea.Cmd {
	return m.toggleScratch(m.builtinScratch())
}

func (m *OS) toggleScratch(spec scratchSpec) tea.Cmd {
	plan := m.planScratch(spec.Name)
	switch plan.action {
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

func (m *OS) rememberScratchReturn() {
	m.scratchReturnMode = m.chosenMode()
	m.scratchReturnID = ""
	if w := m.GetFocusedWindow(); w != nil && !isScratch(w) {
		m.scratchReturnID = w.ID
	}
}

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

func (m *OS) leaveScratchView() {
	if !m.InScratchView() {
		return
	}
	base := max(m.scratchBase, 1)
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

func (m *OS) leaveScratchWorkspace() {
	m.AutoTiling = m.scratchBaseTiling
	m.scratchBase = 0
	m.scratchViewName = ""
}

func (m *OS) scratchNameOnWorkspace(ws int) (string, bool) {
	for _, w := range m.Windows {
		if w.Workspace == ws && isScratch(w) {
			return scratchNameOf(w), true
		}
	}
	return "", false
}

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

func (m *OS) syncScratchView() {
	if !m.hasFocusedWindow() {
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

func (m *OS) scratchDir() string {
	raw := ""
	if w := m.GetFocusedWindow(); w != nil && w.Host == "" {
		raw = paneDir(w)
	}
	return localFolder(raw)
}

func localFolder(raw string) string {
	if dir, _ := localCwdPath(raw); dir != "" {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

func (m *OS) createScratch(spec scratchSpec) tea.Cmd {
	if !m.IsDaemonSession || m.DaemonClient == nil {
		m.createLocalScratch(m.scratchDir(), spec)
		return nil
	}
	var dir, from string
	if m.AttachedHost != "" {
		dir = m.remotePaneDir()
	} else {
		dir = m.scratchDir()
		if w := m.GetFocusedWindow(); w != nil && w.Host == "" {
			from = w.ID
		}
	}
	m.scratchPending = spec.Name
	m.scratchPendingAt = time.Now()
	req := scratchRequest{
		Session:   m.SessionName,
		Name:      spec.Name,
		Title:     spec.Title,
		Command:   spec.Command,
		Dir:       dir,
		DirFrom:   from,
		Width:     spec.Width,
		Height:    spec.Height,
		Workspace: m.CurrentWorkspace,
		Host:      m.AttachedHost,
		Build:     m.DaemonClient.ClientVersion(),
	}
	label, name := spec.Title, spec.Name
	return func() tea.Msg {
		return ScratchOpenedMsg{Label: label, Name: name, Err: scratchOpener(req)}
	}
}

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

func (m *OS) newLocalPopup(dir, title, widthSpec, heightSpec string, command []string) *terminal.Window {
	probe := &terminal.Window{PopupWidth: widthSpec, PopupHeight: heightSpec}
	x, y, width, height := m.popupRect(probe)

	id := createID()
	caps := m.hostCaps()
	w, err := terminal.NewWindowInCell(dir, id, title, x, y, width, height, len(m.Windows),
		m.WindowExitChan, m.PTYDataChan, m.Settings.ScrollbackLines, caps.CellWidth, caps.CellHeight, command...)
	if err != nil {
		m.LogError("Failed to create the popup %s: %v", title, err)
		m.ShowNotification("The popup did not open: "+err.Error(), "error", m.Settings.NotificationDuration)
		return nil
	}
	if caps.CellWidth > 0 && caps.CellHeight > 0 {
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

type scratchRequest struct {
	Session       string
	Name, Title   string
	Command       []string
	Dir           string
	DirFrom       string
	Width, Height string
	Workspace     int
	Host, Build   string
}

func openScratchPopup(req scratchRequest) error {
	c, err := dialSessionDaemon(req.Host, req.Build)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	params := map[string]any{
		"session":      req.Session,
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
	if req.DirFrom != "" {
		params["cwd_from"] = req.DirFrom
	}
	params["wait"] = true
	params["timeout"] = int(scratchStoppedWait / time.Millisecond)
	raw, err := c.CallWithTimeout("popup", params, scratchStoppedWait+5*time.Second)
	if session.VerbErrCode(err) == session.ErrVerbTimeout {
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
	if jsonx.Unmarshal(raw, &res) == nil && res.Type == "popup_result" {
		return ScratchStoppedError{Code: res.ExitCode, WindowID: res.WindowID}
	}
	return nil
}

func (m *OS) handleScratchOpened(msg ScratchOpenedMsg) {
	if msg.Err == nil {
		return
	}
	m.scratchPending = ""
	if scratchExists(msg.Err) && msg.Name != "" {
		m.scratchPending = msg.Name
		m.scratchPendingAt = time.Now()
		m.maybeFocusScratch()
		return
	}
	if stopped, ok := errors.AsType[ScratchStoppedError](msg.Err); ok {
		if stopped.WindowID != "" {
			if m.deadScratch == nil {
				m.deadScratch = map[string]bool{}
			}
			m.deadScratch[stopped.WindowID] = true
		}
		m.ShowNotification(fmt.Sprintf("The command %s stopped with exit code %d.", msg.Label, stopped.Code), "error", m.Settings.NotificationDuration)
		return
	}
	why := m.sessionCallFailure(msg.Err)
	if why == "" {
		why = plainCause(msg.Err)
	}
	m.ShowNotification("The scratch terminal did not open. "+why, "error", m.Settings.NotificationDuration*2)
}

func scratchExists(err error) bool {
	callErr, ok := errors.AsType[*session.VerbCallError](err)
	return ok && callErr.Code == session.ErrVerbInvalidParams && callErr.Hint != nil &&
		callErr.Hint.Param == "scratch" && callErr.Message == session.ErrScratchExists.Error()
}

func (m *OS) maybeFocusScratch() {
	if m.scratchPending == "" {
		return
	}
	if time.Since(m.scratchPendingAt) >= m.scratchPendingLimit() {
		m.scratchPending = ""
		return
	}
	i := m.scratchIndexNamed(m.scratchPending)
	if i < 0 {
		return
	}
	m.scratchPending = ""
	m.showScratch(i)
}

func (m *OS) windowCountForNotice() int {
	n := 0
	for _, w := range m.Windows {
		if !hiddenFromLists(w) {
			n++
		}
	}
	return n
}

func (m *OS) panesMadeForNotice() int {
	n := 0
	for _, w := range m.Windows {
		if !hiddenFromLists(w) && w.Kind != session.WindowKindTML {
			n++
		}
	}
	return n
}

func (m *OS) windowLabelsForNotice() map[string]string {
	labels := make(map[string]string, len(m.Windows))
	for _, w := range m.Windows {
		if !hiddenFromLists(w) {
			labels[w.ID] = windowRowTitle(w)
		}
	}
	return labels
}

func (m *OS) windowMoveNotice(state *session.SessionState, before map[string]string) (string, bool) {
	if state == nil || len(state.WindowMoves) == 0 {
		return "", false
	}
	after := m.windowLabelsForNotice()
	for id, label := range before {
		if _, ok := after[id]; ok {
			continue
		}
		if mv, ok := state.LastWindowMove(id); ok && mv.Away {
			return fmt.Sprintf("Moved %s to the session %s.", label, mv.Other), true
		}
	}
	for id, label := range after {
		if _, ok := before[id]; ok {
			continue
		}
		if mv, ok := state.LastWindowMove(id); ok && !mv.Away {
			return fmt.Sprintf("Moved %s here from the session %s.", label, mv.Other), true
		}
	}
	return "", false
}

// HideShownScratch hides the scratch group when it is on the screen, and
// gives the focus back as the key does. It reports whether it hid one.
func (m *OS) HideShownScratch() bool {
	if !m.InScratchView() {
		return false
	}
	m.leaveScratchView()
	return true
}

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

const scratchBackdropZ = -10000

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

func (m *OS) dockWorkspace() int {
	if m.InScratchView() {
		return max(m.scratchBase, 1)
	}
	return m.CurrentWorkspace
}
