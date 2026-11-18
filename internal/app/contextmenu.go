package app

import (
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
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

// fileMenuTarget is the listing row a files-section menu was opened on.
//
// It is captured when the menu opens and carried to the row that runs, so an
// action reached from the menu acts on the name the user pointed at rather than
// on the row the keyboard cursor happens to be sitting on. Right-clicking a row
// does not move the cursor, and it must not have to.
//
// Dir is the folder that was listed at that moment, kept for the same reason
// filePromptState keeps one: a reply already in flight can replace the listing
// between the right-click and the row being chosen, and the operation has to act
// on what the user was looking at.
type fileMenuTarget struct {
	Dir string
	// Name is the entry, or "" when the menu was opened on the section's blank
	// space, on its header, or on a row whose name has since left the listing.
	Name string
	// IsDir says the entry is a folder, which is the whole of what a folder's
	// menu and a file's have to differ by.
	IsDir bool
	// Up marks the ".." row. It names the folder above, which is not a name in
	// the listing and so is not a target for a rename or a delete.
	Up bool
}

// Active reports whether the target names something a menu can be about at all.
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
// Where it was drawn is not kept here: the renderer puts the panel and each
// row it drew into the frame's hit record (placeContextMenu), and the mouse
// asks the record (contextMenuRowAt, contextMenuContains).
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

	// ScrollFrom is the first row the last frame actually drew. The rows the
	// hit record holds are the ones drawn, so a click resolves against the
	// rows the user can see, not against a List.Scroll that may have moved
	// since.
	ScrollFrom int

	// drawn is set when a frame has put this menu into the hit record. A
	// menu that replaces another (a submenu) is not drawn yet, and the
	// record still holds the rows of the one before it, which are not its
	// rows.
	drawn bool

	// depth counts the submenus opened in a row to reach this menu.
	depth int
}

// selectable reports whether row i can be highlighted and run.
func (cm *ContextMenu) selectable(i int) bool {
	return i >= 0 && i < len(cm.Items) && !cm.Items[i].Sep && !cm.Items[i].Dim
}

// home puts the cursor on the first runnable row, which may not be row zero
// (a dimmed action or a separator), and the window at the top. A menu with no
// runnable row has its cursor at -1.
func (cm *ContextMenu) home() {
	cm.List = listnav.State{Cursor: -1}
	for i := range cm.Items {
		if cm.selectable(i) {
			cm.List.Cursor = i
			return
		}
	}
}

// selectWarn lands the selection on the menu's first destructive row, for the
// callers whose whole intent is that row (the rail's kill key). A menu with no
// destructive row, or whose only one is dimmed, keeps the selection it opened
// with rather than jumping somewhere arbitrary.
func (cm *ContextMenu) selectWarn() {
	for i, it := range cm.Items {
		if it.Warn && cm.selectable(i) {
			cm.List.Cursor = i
			return
		}
	}
}

// contextMenuRowAt returns the menu row drawn at screen (x, y), or -1 when
// the point is not on a runnable row: outside the menu, on its chrome, or on
// a separator or dimmed row.
func (m *OS) contextMenuRowAt(x, y int) int {
	cm := m.ContextMenu
	s, h, ok := m.hits.at(x, y)
	if cm == nil || !cm.drawn || !ok || s != hitMenu || !cm.selectable(h.ref) {
		return -1
	}
	return h.ref
}

// contextMenuContains reports whether a screen cell falls anywhere on the
// menu as drawn, chrome included. A click outside is what dismisses the menu.
func (m *OS) contextMenuContains(x, y int) bool {
	if m.ContextMenu == nil || !m.ContextMenu.drawn {
		return false
	}
	s, _, ok := m.hits.at(x, y)
	return ok && s == hitMenu
}

// ContextMenuRowRect returns where row i of the open menu was drawn on the
// last frame, in screen cells, and false when it was not drawn.
func (m *OS) ContextMenuRowRect(i int) (overlay.Rect, bool) {
	if m.ContextMenu == nil || !m.ContextMenu.drawn || i < 0 {
		return overlay.Rect{}, false
	}
	return m.hits.rect(hitMenu, i)
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

// carryMenuTarget hands what the menu was opened on to the dispatch of the row
// being taken. See TakeMenuWorkspace.
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
	// The list rule, over the rows that can be run: the menu wrapped at its
	// ends before any other list did, and now it does so only when the lists
	// do.
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
	if !m.contextMenuContains(x, y) {
		m.CloseContextMenu()
		return "", true
	}
	idx := m.contextMenuRowAt(x, y)
	if idx < 0 {
		// On the menu but not on a runnable row (chrome, separator, dimmed):
		// swallow the click so it cannot reach the pane underneath.
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
	if idx := m.contextMenuRowAt(x, y); idx >= 0 {
		n := len(cm.Items)
		cm.List.Select(idx, n, m.contextMenuVisible(n))
	}
}

// ============================================================================
// Hints, read from the keybind registry
// ============================================================================

// subPrefixTables maps each sub-prefix table to the action that opens it. A
// sub-prefix action's own key is only the last keystroke of its chord, so the
// key of the action that leads there is looked up too. Both lookups go
// through the registry, so the whole chord follows a rebind.
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

// windowModeTables are the tables window management mode reads its keys from.
var windowModeTables = []config.Table{
	config.TableWindowManagement, config.TableWorkspaces, config.TableLayout,
	config.TableModeControl, config.TableSystem, config.TableNavigation,
	config.TableRestoreMinimized,
}

// contextMenuHint returns the one key that runs action in mode, formatted the
// way a person types it. It reads the live registry, so a rebound action shows
// its new key and an unbound one shows no hint at all.
//
// A key of the mode itself comes first (window mode's tables, or terminal
// mode's), then the global keys, then the prefix chord, which works in either
// mode: the leader, for a sub-prefix action the key that opens its sub-prefix,
// then the action's own key. The rail's keys come last (see firstKey). An
// action with none of these in terminal mode, such as rename_window, shows no
// hint there: its window-mode key would reach the pane. An
// action has one name now, and new_window has a window-mode key as well as a
// prefix one, so the mode is what picks between them; the name no longer can.
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
			return "" // no way in, so there is no chord to show
		}
		return s.LeaderKey + " " + entry[0] + " " + k
	}
	// In window mode any other table's key still works (GetKeys sweeps
	// them). In terminal mode it would be typed into the pane, so only the
	// rail's own keys, which act while the rail has the keyboard, are left.
	if mode != TerminalMode {
		return firstKey(registry, action)
	}
	if k := first(config.TableSidebarFiles, config.TableSidebar); k != "" {
		return k
	}
	return ""
}

// firstKey returns the first key bound to an action, or "".
//
// GetKeys sweeps the window, prefix and terminal sections and stops there, so
// an action that lives only in one of the rail's two scopes would answer with
// no hint at all. Those two are asked after it rather than folded into it: they
// are the rail's own keymaps, they are consulted only while the rail owns the
// keyboard, and a rail key must not start showing up as the hint for a
// window-mode action that happens to share its name.
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
