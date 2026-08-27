package app

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
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
// BoundsX/Y/W/H, FirstRowY and ItemH are written by the renderer each frame.
// Together they let HitTest map a screen Y to a row index by arithmetic instead
// of walking rows, and let the click handler tell a click on the menu from a
// click away from it.
type ContextMenu struct {
	Title  string
	Target ContextMenuTarget
	Items  []ContextMenuItem
	// Selected is the highlighted row, or -1 when nothing is selectable.
	Selected int
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

	// Scroll is the first item shown when the menu has more rows than the screen
	// has room for. ScrollFrom is what the last frame actually drew from, and is
	// what hit-testing maps against: a click has to resolve against the rows the
	// user can see, not against a scroll offset that may have moved since.
	Scroll     int
	ScrollFrom int
}

// selectable reports whether row i can be highlighted and run.
func (cm *ContextMenu) selectable(i int) bool {
	return i >= 0 && i < len(cm.Items) && !cm.Items[i].Sep && !cm.Items[i].Dim
}

// Next returns the nearest selectable row in the given direction (+1 or -1),
// wrapping at the ends. Separators and dimmed rows are stepped over, so arrow
// keys never land on something that cannot be run.
func (cm *ContextMenu) Next(dir int) int {
	if len(cm.Items) == 0 {
		return -1
	}
	i := cm.Selected
	for range cm.Items {
		i = (i + dir + len(cm.Items)) % len(cm.Items)
		if cm.selectable(i) {
			return i
		}
	}
	return cm.Selected
}

// Move highlights the next selectable row in the given direction.
func (cm *ContextMenu) Move(dir int) {
	cm.Selected = cm.Next(dir)
}

// selectWarn lands the selection on the menu's first destructive row, for the
// callers whose whole intent is that row (the rail's kill key). A menu with no
// destructive row, or whose only one is dimmed, keeps the selection it opened
// with rather than jumping somewhere arbitrary.
func (cm *ContextMenu) selectWarn() {
	for i, it := range cm.Items {
		if it.Warn && cm.selectable(i) {
			cm.Selected = i
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
	// The first drawn row is item ScrollFrom, not item zero, whenever the menu
	// is taller than the screen and has scrolled.
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
	if cm == nil || !cm.selectable(cm.Selected) {
		return ""
	}
	m.menuWorkspace, m.menuSession, m.menuFile = cm.Workspace, cm.SessionID, cm.File
	return cm.Items[cm.Selected].Action
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

// ClearMenuTarget ends the carry. The input layer calls it after dispatching a
// menu row.
func (m *OS) ClearMenuTarget() {
	m.menuWorkspace, m.menuSession, m.menuFile = 0, "", fileMenuTarget{}
}

// ContextMenuMove moves the selection by delta, skipping separators and dimmed
// rows.
func (m *OS) ContextMenuMove(delta int) {
	if m.ContextMenu == nil {
		return
	}
	dir := 1
	if delta < 0 {
		dir = -1
	}
	for range absInt(delta) {
		m.ContextMenu.Move(dir)
	}
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
		// On the menu but not on a runnable row (chrome, separator, dimmed):
		// swallow the click so it cannot reach the pane underneath.
		return "", true
	}
	cm.Selected = idx
	action = cm.Items[idx].Action
	m.menuWorkspace, m.menuSession, m.menuFile = cm.Workspace, cm.SessionID, cm.File
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
		cm.Selected = idx
	}
}

// absInt is the absolute value of an int.
func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// ============================================================================
// Hints, read from the keybind registry
// ============================================================================

// subPrefixEntry maps an action name prefix to the action that enters the
// prefix mode it lives in. A sub-prefix action's own key is only the last
// keystroke of its chord, so the keys of the actions that lead there have to be
// looked up too. Both lookups go through the registry, so the whole chord
// follows a rebind.
var subPrefixEntry = map[string]string{
	"window_prefix_":    "prefix_window",
	"minimize_prefix_":  "prefix_minimize",
	"workspace_prefix_": "prefix_workspace",
	"debug_prefix_":     "prefix_debug",
	"tape_prefix_":      "prefix_tape",
}

// contextMenuHint returns the key currently bound to an action, formatted the
// way a user would type it. It reads the live registry, so a rebound action
// shows its new key and an unbound one shows no hint at all.
//
// Prefix-mode actions are stored under their bare chord key ("c"), because the
// leader is what got the user into prefix mode in the first place. The leader,
// and for a sub-prefix action the key that opens that sub-prefix, are prepended
// here so the hint is the whole thing a user has to press.
func contextMenuHint(registry *config.KeybindRegistry, action string) string {
	if registry == nil || action == "" {
		return ""
	}
	key := firstKey(registry, action)
	if key == "" {
		return ""
	}
	for name, entry := range subPrefixEntry {
		if !strings.HasPrefix(action, name) {
			continue
		}
		entryKey := firstKey(registry, entry)
		if entryKey == "" {
			return "" // no way in, so there is no chord to show
		}
		return config.LeaderKey + " " + entryKey + " " + key
	}
	if strings.HasPrefix(action, "prefix_") {
		return config.LeaderKey + " " + key
	}
	return key
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
		registry.GetSidebarFilesKeys(action),
		registry.GetSidebarKeys(action),
	} {
		if len(keys) > 0 {
			return keys[0]
		}
	}
	return ""
}
