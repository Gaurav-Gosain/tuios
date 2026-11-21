package app

import (
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
)

// ContextMenuTarget names the thing a context menu was opened on. What is under
// the pointer decides what the menu offers, so the target is resolved once when
// the menu opens and the item list is built from it.
type ContextMenuTarget int

// A pane is one target, not two.
//
// There was briefly a separate target for a pane's title row. It was a bad
// target twice over. It was one row tall, and it was the top border, which is
// not the row a user reads as the title bar: appearance.window_title_position
// defaults to "bottom", so the window's name is drawn on the opposite edge from
// the row that opened the menu. Aiming at the name got the content menu and
// aiming at the name's opposite edge got the title menu.
//
// Every action it offered now lives on the pane menu, which the whole surface
// of a pane opens, title row included. A second target that is hard to hit and
// offers nothing unique is worse than not having one.
const (
	// CtxTargetDesktop is empty space with no pane under it.
	CtxTargetDesktop ContextMenuTarget = iota
	// CtxTargetPane is anywhere on a pane, its border rows included.
	CtxTargetPane
	// CtxTargetDock is the dock bar away from any of its entries.
	CtxTargetDock
	// CtxTargetDockItem is one minimized-window entry in the dock.
	CtxTargetDockItem
	// CtxTargetWorkspacePill is one workspace tab in the dock's strip. The "+"
	// tab is not one: it stands for a workspace that does not exist yet.
	CtxTargetWorkspacePill
	// CtxTargetMachine is a machine's group header on the rail. What the menu
	// is about rides in ContextMenu.SessionID, which holds the host name.
	CtxTargetMachine
	// CtxTargetRepo is a repository's group header on the rail. What the menu
	// is about rides in ContextMenu.Repo, which holds the repository's name.
	CtxTargetRepo
	// CtxTargetFileRow is the rail's files section: one row of the listing, or
	// the blank space the section drew around it. Both are the same target
	// because the two rows that need no file at all (make one, paste one) are
	// the whole of what the blank space can offer, and they are on the row menu
	// too. What the menu was opened on rides in ContextMenu.File.
	CtxTargetFileRow
)

type fileMenuTarget struct {
	Dir   string
	Name  string
	IsDir bool
	Up    bool
}

func (t fileMenuTarget) Active() bool { return t.Dir != "" }

// ContextMenuItem is one row of a context menu.
//
// Action is a keybind-registry action ID, and it is the only thing the row
// knows how to do: the input layer hands it straight to the same
// ActionDispatcher that keybindings dispatch through. A row carries no closure
// of its own, so a menu entry cannot drift from the key that runs it.
//
// Hint is the key currently bound to Action, read from the registry when the
// menu is built rather than written out here. Rebinding an action changes the
// hint; an action with nothing bound to it shows no hint at all.
//
// Dim marks an action that exists but has nothing to act on right now. A dimmed
// row is drawn greyed and skipped by arrow navigation and by hit-testing, so it
// is visible without being reachable. Hiding it instead would change the menu's
// shape from one open to the next and hide the fact that the action exists.
type ContextMenuItem struct {
	Icon   string
	Label  string
	Action string
	Hint   string
	Sep    bool
	Dim    bool
	// Warn draws the label in the destructive color.
	Warn bool
}

// ContextMenu is an open context menu: its rows, the selection, the screen cell
// it was anchored at, and where it actually landed after placement.
//
// BoundsX/Y/W/H, FirstRowY and ItemH are written by the renderer each frame.
// Together they let HitTest map a screen Y to a row index by arithmetic instead
// of walking rows, and let the click handler tell a click on the menu from a
// click away from it.
type ContextMenu struct {
	Title  string
	Target ContextMenuTarget
	Items  []ContextMenuItem
	// List is the menu's cursor and window. Its Cursor is the highlighted row,
	// or -1 when nothing is selectable; its Scroll is the first row shown when
	// the menu has more rows than the screen has room for.
	List listnav.State
	// WindowIndex is the pane or minimized window the menu was opened on, or -1
	// for the desktop and the dock background.
	WindowIndex int
	// Workspace is the workspace pill the menu was opened on, or 0. A menu row
	// runs after the menu has closed, so this is handed to the dispatch through
	// menuWorkspace rather than read off the menu by the handler.
	Workspace int
	// SessionID is the session row the menu was opened on, or "". It carries the
	// same way Workspace does, so a row acting on a session acts on the one the
	// user pointed at rather than on the attached one.
	SessionID string
	// Repo is the repository whose group header the menu was opened on, or "".
	// It carries the same way SessionID does, and is its own field because a
	// repository is not a session: a row acting on one cannot be handed the
	// other by mistake.
	Repo string
	// File is the listing row the menu was opened on, for a files-section menu.
	// It carries exactly as far as Workspace and SessionID do, and it is read
	// only inside this package: the actions it steers (fileActionTarget,
	// SidebarFileOpen) live here, so there is no getter for the input layer the
	// way TakeMenuSession is one.
	File fileMenuTarget

	AnchorX int
	AnchorY int

	BoundsX   int
	BoundsY   int
	BoundsW   int
	BoundsH   int
	FirstRowY int
	ItemH     int

	// ScrollFrom is the first row the last frame actually drew, and is what
	// hit-testing maps against: a click has to resolve against the rows the
	// user can see, not against a List.Scroll that may have moved since.
	ScrollFrom int

	depth int
}

func (cm *ContextMenu) selectable(i int) bool {
	return i >= 0 && i < len(cm.Items) && !cm.Items[i].Sep && !cm.Items[i].Dim
}

func (cm *ContextMenu) home() {
	cm.List = listnav.State{Cursor: -1}
	for i := range cm.Items {
		if cm.selectable(i) {
			cm.List.Cursor = i
			return
		}
	}
}

func (cm *ContextMenu) selectWarn() {
	for i, it := range cm.Items {
		if it.Warn && cm.selectable(i) {
			cm.List.Cursor = i
			return
		}
	}
}

// HitTest returns the row index at screen (x, y), or -1 when the point is not
// on a runnable row: outside the menu, on its chrome, or on a separator or
// dimmed row. Coordinates are absolute screen cells.
func (cm *ContextMenu) HitTest(x, y int) int {
	if !cm.Contains(x, y) {
		return -1
	}
	if cm.ItemH <= 0 {
		return -1
	}
	idx := cm.ScrollFrom + (y-cm.FirstRowY)/cm.ItemH
	if y < cm.FirstRowY || idx < 0 || idx >= len(cm.Items) {
		return -1
	}
	if !cm.selectable(idx) {
		return -1
	}
	return idx
}

// Contains reports whether a screen cell falls anywhere on the menu, chrome
// included. A click outside is what dismisses the menu.
func (cm *ContextMenu) Contains(x, y int) bool {
	return x >= cm.BoundsX && x < cm.BoundsX+cm.BoundsW &&
		y >= cm.BoundsY && y < cm.BoundsY+cm.BoundsH
}

// ContextMenuActive reports whether a context menu is on screen.
func (m *OS) ContextMenuActive() bool {
	return m.ContextMenu != nil
}

// CloseContextMenu dismisses the menu without running anything.
func (m *OS) CloseContextMenu() {
	m.ContextMenu = nil
}

// ContextMenuSelectedAction returns the action ID of the highlighted row, or ""
// when nothing runnable is selected. The caller dispatches it through the
// action dispatcher; this type never runs anything itself.
func (m *OS) ContextMenuSelectedAction() string {
	cm := m.ContextMenu
	if cm == nil || !cm.selectable(cm.List.Cursor) {
		return ""
	}
	m.carryMenuTarget(cm)
	return cm.Items[cm.List.Cursor].Action
}

func (m *OS) carryMenuTarget(cm *ContextMenu) {
	m.menuWorkspace, m.menuSession, m.menuRepo, m.menuFile = cm.Workspace, cm.SessionID, cm.Repo, cm.File
	m.menuFrom = cm
}

// TakeMenuWorkspace returns the workspace whose pill menu produced the action
// being dispatched, or 0. It is set only when a row is actually taken and
// cleared as soon as that one dispatch is over, so an action reached by
// keybinding always sees 0 and falls back to the workspace the user is on.
func (m *OS) TakeMenuWorkspace() int { return m.menuWorkspace }

// TakeMenuSession returns the session whose row menu produced the action being
// dispatched, or "". It carries exactly as far as TakeMenuWorkspace does, so the
// same action reached by key falls back to the attached session.
func (m *OS) TakeMenuSession() string { return m.menuSession }

// TakeMenuRepo returns the repository whose group header menu produced the
// action being dispatched, or "". It carries exactly as far as
// TakeMenuSession does.
func (m *OS) TakeMenuRepo() string { return m.menuRepo }

// ClearMenuTarget ends the carry. The input layer calls it after dispatching a
// menu row.
func (m *OS) ClearMenuTarget() {
	m.menuWorkspace, m.menuSession, m.menuRepo, m.menuFile = 0, "", "", fileMenuTarget{}
	m.menuFrom = nil
}

// ContextMenuMove moves the selection by delta, skipping separators and dimmed
// rows.
func (m *OS) ContextMenuMove(delta int) {
	cm := m.ContextMenu
	if cm == nil || len(cm.Items) == 0 {
		return
	}
	n := len(cm.Items)
	cm.List.Move(delta, n, m.contextMenuVisible(n), m.listWraps(), cm.selectable)
}

// ContextMenuClick routes a click at screen (x, y) while a menu is open. It
// returns the action to run and whether the menu consumed the event.
//
// A click away from the menu closes it and fires nothing, which is the whole
// point of returning the action separately from the consumed flag.
func (m *OS) ContextMenuClick(x, y int) (action string, consumed bool) {
	cm := m.ContextMenu
	if cm == nil {
		return "", false
	}
	if !cm.Contains(x, y) {
		m.CloseContextMenu()
		return "", true
	}
	idx := cm.HitTest(x, y)
	if idx < 0 {
		return "", true
	}
	action = cm.Items[idx].Action
	m.carryMenuTarget(cm)
	m.CloseContextMenu()
	return action, true
}

// ContextMenuHover highlights the row under the cursor, so moving the pointer
// over the menu tracks like the keyboard selection does.
func (m *OS) ContextMenuHover(x, y int) {
	cm := m.ContextMenu
	if cm == nil {
		return
	}
	if idx := cm.HitTest(x, y); idx >= 0 {
		n := len(cm.Items)
		cm.List.Select(idx, n, m.contextMenuVisible(n))
	}
}

var subPrefixTables = []struct {
	table config.Table
	entry string
}{
	{config.TableWindowPrefix, "prefix_window"},
	{config.TableMinimizePrefix, "prefix_minimize"},
	{config.TableWorkspacePrefix, "prefix_workspace"},
	{config.TableDebugPrefix, "prefix_debug"},
	{config.TableTapePrefix, "prefix_tape"},
	{config.TableLayoutPrefix, "prefix_layout"},
}

var windowModeTables = []config.Table{
	config.TableWindowManagement, config.TableWorkspaces, config.TableLayout,
	config.TableModeControl, config.TableSystem, config.TableNavigation,
	config.TableRestoreMinimized,
}

func contextMenuHint(registry *config.KeybindRegistry, action string, s *config.Settings, mode Mode) string {
	if registry == nil || action == "" {
		return ""
	}
	first := func(tables ...config.Table) string {
		for _, t := range tables {
			if keys := registry.Keys(t, action); len(keys) > 0 {
				return keys[0]
			}
		}
		return ""
	}
	own := windowModeTables
	if mode == TerminalMode {
		own = []config.Table{config.TableTerminalMode}
	}
	if k := first(append(slices.Clone(own), config.TableGlobal)...); k != "" {
		return k
	}
	if k := first(config.TablePrefixMode); k != "" {
		return s.LeaderKey + " " + k
	}
	for _, sp := range subPrefixTables {
		k := first(sp.table)
		if k == "" {
			continue
		}
		entry := registry.Keys(config.TablePrefixMode, sp.entry)
		if len(entry) == 0 {
			return ""
		}
		return s.LeaderKey + " " + entry[0] + " " + k
	}
	if mode != TerminalMode {
		return firstKey(registry, action)
	}
	if k := first(config.TableSidebarFiles, config.TableSidebar); k != "" {
		return k
	}
	return ""
}

func firstKey(registry *config.KeybindRegistry, action string) string {
	for _, keys := range [][]string{
		registry.GetKeys(action),
		registry.Keys(config.TableSidebarFiles, action),
		registry.Keys(config.TableSidebar, action),
	} {
		if len(keys) > 0 {
			return keys[0]
		}
	}
	return ""
}
