package app

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// ContextMenuTarget names the thing a context menu was opened on. What is under
// the pointer decides what the menu offers, so the target is resolved once when
// the menu opens and the item list is built from it.
type ContextMenuTarget int

const (
	// CtxTargetDesktop is empty space with no pane under it.
	CtxTargetDesktop ContextMenuTarget = iota
	// CtxTargetPaneContent is the inside of a pane, below its title row.
	CtxTargetPaneContent
	// CtxTargetPaneTitle is a pane's title row.
	CtxTargetPaneTitle
	// CtxTargetDock is the dock bar away from any of its entries.
	CtxTargetDock
	// CtxTargetDockItem is one minimized-window entry in the dock.
	CtxTargetDockItem
)

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

	AnchorX int
	AnchorY int

	BoundsX   int
	BoundsY   int
	BoundsW   int
	BoundsH   int
	FirstRowY int
	ItemH     int
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
	idx := (y - cm.FirstRowY) / cm.ItemH
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
	return cm.Items[cm.Selected].Action
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
func firstKey(registry *config.KeybindRegistry, action string) string {
	keys := registry.GetKeys(action)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}
