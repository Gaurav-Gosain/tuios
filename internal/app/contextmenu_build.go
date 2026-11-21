package app

import (
	"path/filepath"
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

const (
	glyphCopy     = ""
	glyphPaste    = ""
	glyphNew      = ""
	glyphRename   = ""
	glyphClose    = ""
	glyphMinimize = ""
	glyphZoom     = ""
	glyphSplitV   = ""
	glyphSplitH   = ""
	glyphTiling   = ""
	glyphPalette  = ""
	glyphSettings = ""
	glyphHelp     = ""
	glyphRestore  = ""
	glyphDetach   = ""
	glyphSwitch   = ""
	glyphClear    = ""
	glyphFolder   = ""
	glyphFile     = ""
	glyphCut      = ""
	glyphUp       = ""
	glyphDown     = ""
)

// OpenContextMenu opens the context menu for whatever is under the screen cell
// (x, y). The target decides the menu, so this is the only place that has to
// know what lives where on screen.
//
// Opening on a pane focuses it first, the way clicking it would: every action
// on the menu acts on the focused window, so focusing the pane the user pointed
// at is what makes "Close" close the pane they right-clicked rather than the
// one that happened to have focus. The mode is deliberately left alone, so
// right-clicking from terminal mode does not kick the user out of it.
func (m *OS) OpenContextMenu(x, y int) {
	target, windowIndex, workspace := m.contextMenuTargetAt(x, y)

	if target == CtxTargetPane {
		m.FocusWindow(windowIndex)
	}

	ax, ay := m.ScreenPoint(x, y)
	cm := &ContextMenu{
		Target:      target,
		WindowIndex: windowIndex,
		Workspace:   workspace,
		AnchorX:     ax,
		AnchorY:     ay,
		ItemH:       1,
	}

	switch target {
	case CtxTargetPane:
		cm.Title, cm.Items = m.paneMenu(windowIndex)
	case CtxTargetDockItem:
		cm.Title, cm.Items = m.dockItemMenu(windowIndex)
	case CtxTargetWorkspacePill:
		cm.Title, cm.Items = m.workspacePillMenu(workspace)
	case CtxTargetDock:
		cm.Title, cm.Items = m.dockMenu()
	default:
		cm.Title, cm.Items = m.desktopMenu()
	}

	cm.home()
	m.ContextMenu = cm
}

func (m *OS) contextMenuTargetAt(x, y int) (target ContextMenuTarget, windowIndex, workspace int) {
	if m.InDockBand(y) {
		if idx := m.DockItemAt(x, y); idx >= 0 {
			return CtxTargetDockItem, idx, 0
		}
		if ws := m.DockWorkspacePillAt(x, y); ws > 0 {
			return CtxTargetWorkspacePill, -1, ws
		}
		return CtxTargetDock, -1, 0
	}

	if idx := m.WindowAt(x, y); idx >= 0 {
		return CtxTargetPane, idx, 0
	}
	return CtxTargetDesktop, -1, 0
}

// InDockBand reports whether a screen row falls in the reserved dock band.
// The input layer's hover routing uses it too, because focus-follows-mouse
// must never treat the dock band as a pane.
//
// A dock of DockHeight rows at the top of the screen occupies rows 0 to
// DockHeight-1, so the test is exclusive. DockHeight is one row when the dock
// is compact and two when it is not. Writing it inclusive, as the click
// handler in internal/input still does, claims one row more than the dock draws
// on: with the dock at the top that extra row is the first row of the topmost
// window, which is how the pane menu came to be unreachable there.
func (m *OS) InDockBand(y int) bool {
	_, y = m.ScreenPoint(0, y)
	switch m.Settings.DockbarPosition {
	case "hidden":
		return false
	case "top":
		return y < m.Settings.DockHeight()
	default:
		return y >= m.Height-m.Settings.DockHeight()
	}
}

// WindowAt returns the index of the topmost visible window containing the
// screen cell (x, y), or -1.
func (m *OS) WindowAt(x, y int) int {
	top, topZ := -1, -1
	for i, win := range m.Windows {
		if win == nil || win.Workspace != m.CurrentWorkspace || win.Minimized {
			continue
		}
		if x < win.X || x >= win.X+win.Width || y < win.Y || y >= win.Y+win.Height {
			continue
		}
		if z := HitZ(win); z >= topZ {
			top, topZ = i, z
		}
	}
	return top
}

// DockItemAt returns the window index of the dock entry covering the absolute
// cell (x, y), or -1. It hit-tests the rectangles the renderer recorded while
// drawing, the same way the workspace strip and the message block do, so the
// entry the user clicks is the one they see.
func (m *OS) DockItemAt(x, y int) int {
	for _, h := range m.dockItemHits {
		if y == h.Y && x >= h.X0 && x < h.X1 {
			return h.WindowIndex
		}
	}
	return -1
}

// DockOverflowAt reports whether the absolute cell (x, y) is on the marker
// standing for the minimized panes the bar had no room for.
func (m *OS) DockOverflowAt(x, y int) bool {
	h := m.dockOverflowHit
	return h.Active && y == h.Y && x >= h.X0 && x < h.X1
}

func (m *OS) minimizedPosition(windowIndex int) int {
	for pos, i := range m.MinimizedInOrder() {
		if i == windowIndex {
			return pos
		}
	}
	return -1
}

func contextMenuWindowName(m *OS, windowIndex int, fallback string) string {
	if windowIndex < 0 || windowIndex >= len(m.Windows) || m.Windows[windowIndex] == nil {
		return fallback
	}
	win := m.Windows[windowIndex]
	if win.CustomName != "" {
		return win.CustomName
	}
	if title := win.Title(); title != "" && !isDefaultTitle(title, win.ID) {
		return title
	}
	return fallback
}

//go:noinline
func (m *OS) item(icon, label, action string, dim bool) ContextMenuItem {
	return ContextMenuItem{
		Icon:   icon,
		Label:  label,
		Action: action,
		Hint:   contextMenuHint(m.KeybindRegistry, action, &m.Settings, m.Mode),
		Dim:    dim,
	}
}

func separator() ContextMenuItem { return ContextMenuItem{Sep: true} }

func (m *OS) paneMenu(windowIndex int) (string, []ContextMenuItem) {
	var win *terminal.Window
	if windowIndex >= 0 && windowIndex < len(m.Windows) {
		win = m.Windows[windowIndex]
	}

	return contextMenuWindowName(m, windowIndex, "Pane"), m.menu("pane",
		is(win.HasSelection(), mfSelection)|
			is(m.Mode == TerminalMode && m.ClipboardReadUnsupportedReason() == "", mfCanPaste)|
			is(m.AutoTiling, mfCanSplit)|
			is(isScratch(win), mfScratch)|
			is(m.SidebarActive(), mfSidebar))
}

func (m *OS) dockItemMenu(windowIndex int) (string, []ContextMenuItem) {
	return contextMenuWindowName(m, windowIndex, "Window"), m.dockItemRows(m.minimizedPosition(windowIndex))
}

func (m *OS) workspacePillMenu(ws int) (string, []ContextMenuItem) {
	title := printableTitle(m.WorkspaceLabel(ws))
	if m.Settings.Simple && m.workspaceGivenName(ws) == "" {
		title = m.WorkspaceWordTitle() + " " + title
	}
	return title, m.menu("workspace", is(ws == m.CurrentWorkspace, mfWsCurrent)|is(len(m.workspacePanes(ws)) == 0, mfWsEmpty))
}

func (m *OS) dockMenu() (string, []ContextMenuItem) {
	return "Dock", m.menu("dock", is(m.HasMinimizedWindows(), mfMinimized))
}

func (m *OS) desktopMenu() (string, []ContextMenuItem) {
	return "Desktop", m.menu("desktop", 0)
}

func (m *OS) sessionMenu(sessionID string) (string, []ContextMenuItem) {
	if sessionID == "" {
		sessionID = m.SessionName
	}
	title := m.SessionLabel(sessionID)
	if title == "" {
		title = "Session"
	}
	attached := sessionID == m.SessionName
	repo := m.sidebarHeadRepo(sessionID)
	return title, m.menu("session",
		is(attached, mfAttached)|
			is(len(m.otherSessionNames()) > 0, mfOthers)|
			is(attached && m.sessionDeletableWorktree(m.SessionName) != nil, mfDeletable)|
			is(m.sessionWorktreeInfo(sessionID) == nil, mfNoWorktree)|
			is(repo != "", mfHeadRepo)|
			is(repo != "" && m.SidebarRepoCollapsed(repo), mfCollapsed))
}

func (m *OS) repoHeaderMenu(repo string) (string, []ContextMenuItem) {
	title := printableTitle(repo)
	if title == "" {
		title = "Repository"
	}
	return title, m.menu("repo", mfHeadRepo|is(m.worktreeRepoRoot(repo) == "", mfNoRepoRoot)|is(m.SidebarRepoCollapsed(repo), mfCollapsed))
}

// OpenSelectionMenu opens the small terminal-mode selection menu: what you can
// do with an active text selection, and nothing else. It exists because a
// plain right-click in terminal mode only means anything when there is a
// selection; without one the click belongs to the application in the pane.
func (m *OS) OpenSelectionMenu(x, y, windowIndex int) {
	m.FocusWindow(windowIndex)
	cm := &ContextMenu{
		Target:      CtxTargetPane,
		WindowIndex: windowIndex,
		AnchorX:     x,
		AnchorY:     y,
		ItemH:       1,
	}
	cm.Title = "Selection"
	cm.Items = m.menu("selection", 0)
	cm.home()
	m.ContextMenu = cm
}

func (m *OS) machineMenu(host string) (string, []ContextMenuItem) {
	at := slices.Index(m.SidebarHostIDs, host)
	first := at <= 0
	last := at < 0 || at >= len(m.SidebarHostIDs)-1
	title := printableTitle(host)
	if title == "" {
		title = "Machine"
	}
	return title, m.menu("machine", is(first, mfFirst)|is(last, mfLast)|is(host == federation.LocalHostName && m.DaemonRestartAvailable(), mfRestart))
}

func (m *OS) fileRowMenu(t fileMenuTarget) (string, []ContextMenuItem) {
	on := m.FileActionsOn()
	hasTarget := on && t.Name != ""
	canOpen := m.filesOn() && (t.Name != "" || t.Up)

	return fileMenuTitle(t), m.menu("file",
		is(t.Up, mfUp)|is(t.IsDir, mfDir)|is(t.Name != "", mfNamed)|
			is(on, mfOn)|is(hasTarget, mfTarget)|is(canOpen, mfCanOpen)|
			is(m.fileClip.Empty(), mfClipEmpty)|is(m.GetFocusedWindow() == nil, mfNoFocus))
}

func fileMenuTitle(t fileMenuTarget) string {
	name := t.Name
	if t.Up {
		name = ".."
	}
	if name == "" && t.Dir != "" {
		name = filepath.Base(t.Dir)
	}
	if title := printableTitle(name); title != "" {
		return title
	}
	if name != "" {
		return name
	}
	return "Files"
}
