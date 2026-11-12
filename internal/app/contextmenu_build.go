package app

import (
	"path/filepath"
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Row glyphs. These are Nerd Font codepoints, written as escapes so the source
// stays readable in an editor without the font installed. They are dropped
// wholesale when the user has asked for ASCII-only output.
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

	// The menu is drawn on the screen, so it is anchored where the pointer
	// is on the screen, even when (x, y) is a pane's position in the layout
	// of a larger session. See pane_view.go.
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

	// Start on the first runnable row rather than on row zero, which may be a
	// dimmed action or a separator.
	cm.home()
	m.ContextMenu = cm
}

// contextMenuTargetAt classifies the screen cell (x, y) into a menu target, and
// returns the window or dock entry it belongs to (-1 when it belongs to
// neither).
//
// The order matches the one handleMouseClick already uses, so the menu opens on
// the same thing an ordinary click would act on: the dock band is reserved and
// wins over any window drawn near it, then the topmost window under the point,
// then the desktop.
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

	// Anywhere on a pane, border rows included, opens that pane's menu. The
	// title row is not a target of its own; see the note on the target
	// constants.
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

// minimizedPosition returns the position of a window among the minimized
// windows of the current workspace, counting from zero, or -1 when it is not
// minimized. This is the index the restore_minimized_N actions count in,
// and the dock's order (MinimizedInOrder).
func (m *OS) minimizedPosition(windowIndex int) int {
	for pos, i := range m.MinimizedInOrder() {
		if i == windowIndex {
			return pos
		}
	}
	return -1
}

// ============================================================================
// Per-target menus
// ============================================================================

// contextMenuWindowName titles a menu after the window it acts on, so a menu
// opened on one of several panes says which one it will affect.
//
// It resolves the name the same way the title bar does: the user's own name for
// the window first, then a title the program inside it set, ignoring the
// "Terminal <id>" default because naming a menu after that says nothing. The
// fallback is the caller's generic word.
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

// item builds a row, resolving its key hint from the live registry.
//
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

// separator is a divider row.
func separator() ContextMenuItem { return ContextMenuItem{Sep: true} }

// paneMenu is the menu for a pane, opened from anywhere on it, border rows
// included: what you can do with what is inside it, how to divide it, and what
// to do with the pane itself.
//
// This is deliberately one menu rather than a content menu and a title-bar
// menu. See the note on the target constants for why the title row stopped
// being a target of its own.
func (m *OS) paneMenu(windowIndex int) (string, []ContextMenuItem) {
	// The pane the menu was opened on, not the focused one. Every caller focuses
	// the target before building, so the two agree today; asking the target
	// directly is what keeps them agreeing when a caller stops doing that.
	var win *terminal.Window
	if windowIndex >= 0 && windowIndex < len(m.Windows) {
		win = m.Windows[windowIndex]
	}

	// Pasting reaches the shell only from terminal mode; the clipboard reply is
	// dropped in every other mode. Dimming says so rather than letting the row
	// look live and do nothing.
	//
	// A browser tab never sends the clipboard at all, so the row is dimmed there
	// in every mode. It read as fully live and did nothing when clicked, which
	// is the inert control this project forbids. See ClipboardReadUnsupportedReason.
	//
	// A scratch pane splits and zooms inside its group. It is not minimized:
	// the scratch key hides the whole group. An accent shows on the rail, so
	// there is nothing to set without one. Rename is never dimmed for a hidden
	// title bar: the editor is a centred dialog.
	return contextMenuWindowName(m, windowIndex, "Pane"), m.menu("pane",
		is(win.HasSelection(), mfSelection)|
			is(m.Mode == TerminalMode && m.ClipboardReadUnsupportedReason() == "", mfCanPaste)|
			is(m.AutoTiling, mfCanSplit)|
			is(isScratch(win), mfScratch)|
			is(m.SidebarActive(), mfSidebar))
}

// dockItemMenu is the menu for one minimized window in the dock.
//
// Restoring the nth minimized window is only an action for the first nine of
// them, so a tenth entry shows the row dimmed rather than pretending.
func (m *OS) dockItemMenu(windowIndex int) (string, []ContextMenuItem) {
	return contextMenuWindowName(m, windowIndex, "Window"), m.dockItemRows(m.minimizedPosition(windowIndex))
}

// workspacePillMenu is the menu for one workspace tab in the dock's strip. It
// is where renaming a workspace became findable: the feature existed but was
// reachable only from inside a workspace list, which is not where a user
// looks for it when the thing they want to rename is on screen in front of
// them.
//
// The title is what the tab says, so the menu names the workspace it will act
// on even when that workspace is not the one being shown. Every row acts on the
// tab's workspace, Close included: it raises close_workspace's confirmation for
// this workspace, and is dimmed when the workspace has no pane to close.
func (m *OS) workspacePillMenu(ws int) (string, []ContextMenuItem) {
	return printableTitle(m.WorkspaceLabel(ws)), m.menu("workspace", is(ws == m.CurrentWorkspace, mfWsCurrent)|is(len(m.workspacePanes(ws)) == 0, mfWsEmpty))
}

// dockMenu is the menu for the dock away from any of its entries.
func (m *OS) dockMenu() (string, []ContextMenuItem) {
	return "Dock", m.menu("dock", is(m.HasMinimizedWindows(), mfMinimized))
}

// desktopMenu is the menu for empty space: making something appear, and the
// session-wide overlays.
func (m *OS) desktopMenu() (string, []ContextMenuItem) {
	return "Desktop", m.menu("desktop", 0)
}

// sessionMenu mirrors the quit menu's rows for a sidebar session row: the
// session lifecycle actions, dispatched through the same registry actions the
// keybindings use, so the menu and the quit menu cannot drift apart.
//
// Killing is offered on every row, the row's own session and no other. The two
// attached-session rows say what becomes of this client afterwards, which is a
// question only that session raises: killing any other one leaves this client
// where it is, so it is one row that names what it kills. They used to be the
// same two rows on every session, dimmed everywhere but the attached one,
// because the actions behind them address the attached session whatever row
// they were reached from.
//
// Detaching stays attached-only for the same reason it always was: there is
// nothing to detach from a session this client is not in. The accent belongs to
// the row's own session and is offered on every one of them.
//
// Kill-and-quit on a worktree session takes a directory with it, so it goes
// through the close confirmation — the one dialog that offers to delete the
// worktree too — instead of killing outright. A plain session's row keeps its
// instant quit: there is nothing on disk to ask about.
//
// A main checkout's row is its repository's row too, so it also carries the
// fold row a repository's own row has.
func (m *OS) sessionMenu(sessionID string) (string, []ContextMenuItem) {
	if sessionID == "" {
		sessionID = m.SessionName
	}
	// The menu heads with what the session is called, which is its display name
	// once it has one.
	title := m.SessionLabel(sessionID)
	if title == "" {
		title = "Session"
	}
	attached := sessionID == m.SessionName
	repo := m.sidebarHeadRepo(sessionID)
	// A worktree of the session's repository needs the repository's main
	// checkout, which the rail only knows for a session in a worktree or in
	// the main checkout.
	return title, m.menu("session",
		is(attached, mfAttached)|
			is(len(m.otherSessionNames()) > 0, mfOthers)|
			is(attached && m.sessionDeletableWorktree(m.SessionName) != nil, mfDeletable)|
			is(m.sessionWorktreeInfo(sessionID) == nil, mfNoWorktree)|
			is(repo != "", mfHeadRepo)|
			is(repo != "" && m.SidebarRepoCollapsed(repo), mfCollapsed))
}

// repoHeaderMenu is the menu for a repository's group header on the rail: the
// row that names the repository, which is exactly where a person looks for
// "make another worktree of this one".
//
// What the menu is about rides in ContextMenu.Repo; see TakeMenuRepo.
func (m *OS) repoHeaderMenu(repo string) (string, []ContextMenuItem) {
	title := printableTitle(repo)
	if title == "" {
		title = "Repository"
	}
	// New worktree is live only while some cached session names the repository.
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

// ============================================================================
// The rail's machine groups
// ============================================================================

// machineMenu is the menu for a machine's group header on the rail: where that
// machine's group sits among the others.
//
// It exists for the two move rows. A machine's group is reordered by dragging
// its header up or down, and a drag is a gesture nothing on screen announces,
// so the only people who found it were the ones who guessed. A right-click is
// where a person looks for what a row can do, and it used to open the rail's
// own settings here, which is a menu about the rail rather than about the
// machine that was pointed at.
//
// Both rows run the rail's own reorder actions rather than a body of their
// own, so the menu and the keys bound to those actions cannot drift apart.
// They act on the row under the rail's cursor, which is why the caller puts
// the cursor on the header before building this.
//
// This machine is pinned to the top of the section and its rows are dimmed
// rather than dropped: a dimmed row says the action exists and that this one
// machine cannot use it, and a menu that changed shape from one machine to the
// next would hide that.
func (m *OS) machineMenu(host string) (string, []ContextMenuItem) {
	at := slices.Index(m.SidebarHostIDs, host)
	first := at <= 0
	last := at < 0 || at >= len(m.SidebarHostIDs)-1
	title := printableTitle(host)
	if title == "" {
		title = "Machine"
	}
	// This machine's daemon is the one this client can restart.
	return title, m.menu("machine", is(first, mfFirst)|is(last, mfLast)|is(host == federation.LocalHostName && m.DaemonRestartAvailable(), mfRestart))
}

// ============================================================================
// The rail's files section
// ============================================================================

// fileRowMenu is the menu for the files section: one row of the listing, or the
// blank space around it.
//
// Every row names one of the section's own registry actions, so the hints are
// the keys the user would press on that row and a rebind moves both at once.
// Nothing here runs anything; the input layer hands the action ID to the same
// dispatcher a keypress goes through. See sidebar_file_ops.go for what the
// actions do and for the dialog the two deletes open.
//
// What the target changes:
//
//   - A folder opens. A file's path goes to the clipboard. Those are the two
//     halves of one action, file_open, and they are the whole of what a folder
//     and a file differ by here: the other six work the same on both.
//   - The ".." row names the folder above, which is not a name in the listing.
//     It opens, and it is not a target for a rename, a delete, a copy or a cut.
//   - The blank space and the header name nothing at all, so only the two rows
//     that need no target stay live: make a file, and paste into the folder.
//
// Rows that do not apply are dimmed rather than dropped, which is what paneMenu
// does with a paste it cannot reach and what dockItemMenu does with a restore it
// cannot name. A menu that changed shape from one row to the next would hide the
// fact that the action exists at all.
func (m *OS) fileRowMenu(t fileMenuTarget) (string, []ContextMenuItem) {
	// One gate for the six that touch the disk, the same one the keys ask:
	// the section has to be on screen and the setting has to allow them. With
	// the setting off every one of them is dimmed and the rail's settings row
	// under the menu is where it is turned back on.
	on := m.FileActionsOn()
	hasTarget := on && t.Name != ""
	// Opening is not one of the six. It navigates or copies a path, which is
	// what a plain click on the row already does whether or not file actions
	// are allowed, so it asks only that the section is up.
	canOpen := m.filesOn() && (t.Name != "" || t.Up)

	return fileMenuTitle(t), m.menu("file",
		is(t.Up, mfUp)|is(t.IsDir, mfDir)|is(t.Name != "", mfNamed)|
			is(on, mfOn)|is(hasTarget, mfTarget)|is(canOpen, mfCanOpen)|
			is(m.fileClip.Empty(), mfClipEmpty)|is(m.GetFocusedWindow() == nil, mfNoFocus))
}

// fileMenuTitle heads the menu with what it will act on: the name for a row of
// the listing, and the folder itself when the menu names no row.
//
// The name is laundered the way every other foreign name on the rail is. A name
// off a filesystem can hold a control character, and a menu title is drawn
// straight into the frame.
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
