package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
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
	target, windowIndex := m.contextMenuTargetAt(x, y)

	if target == CtxTargetPane {
		m.FocusWindow(windowIndex)
	}

	cm := &ContextMenu{
		Target:      target,
		WindowIndex: windowIndex,
		AnchorX:     x,
		AnchorY:     y,
		Selected:    -1,
		ItemH:       1,
	}

	switch target {
	case CtxTargetPane:
		cm.Title, cm.Items = m.paneMenu(windowIndex)
	case CtxTargetDockItem:
		cm.Title, cm.Items = m.dockItemMenu(windowIndex)
	case CtxTargetDock:
		cm.Title, cm.Items = m.dockMenu()
	default:
		cm.Title, cm.Items = m.desktopMenu()
	}

	// Start on the first runnable row rather than on row zero, which may be a
	// dimmed action or a separator.
	cm.Selected = cm.Next(1)
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
func (m *OS) contextMenuTargetAt(x, y int) (ContextMenuTarget, int) {
	if m.inDockBand(y) {
		if idx := m.DockItemAt(x, y); idx >= 0 {
			return CtxTargetDockItem, idx
		}
		return CtxTargetDock, -1
	}

	// Anywhere on a pane, border rows included, opens that pane's menu. The
	// title row is not a target of its own; see the note on the target
	// constants.
	if idx := m.WindowAt(x, y); idx >= 0 {
		return CtxTargetPane, idx
	}
	return CtxTargetDesktop, -1
}

// inDockBand reports whether a screen row falls in the reserved dock band.
//
// A dock of DockHeight rows at the top of the screen occupies rows 0 to
// DockHeight-1, so the test is exclusive. Writing it inclusive, as the click
// handler in internal/input still does, claims one row more than the dock draws
// on: with the dock at the top that extra row is the first row of the topmost
// window, which is how the pane menu came to be unreachable there.
func (m *OS) inDockBand(y int) bool {
	switch config.DockbarPosition {
	case "hidden":
		return false
	case "top":
		return y < config.DockHeight
	default:
		return y >= m.Height-config.DockHeight
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
		if win.Z > topZ {
			top, topZ = i, win.Z
		}
	}
	return top
}

// DockItemAt returns the window index of the dock entry at (x, y), or -1. It
// reads the same layout the dock renders from, so the entry the user clicks is
// the one they see.
func (m *OS) DockItemAt(x, y int) int {
	if y != m.GetDockbarContentYPosition() {
		return -1
	}
	for _, pos := range m.CalculateDockLayout().ItemPositions {
		if x >= pos.StartX && x < pos.EndX {
			return pos.WindowIndex
		}
	}
	return -1
}

// minimizedPosition returns the position of a window among the minimized
// windows of the current workspace, counting from zero, or -1 when it is not
// minimized. This is the index the restore_minimized_N actions count in.
func (m *OS) minimizedPosition(windowIndex int) int {
	pos := 0
	for i, win := range m.Windows {
		if win == nil || win.Workspace != m.CurrentWorkspace || !win.Minimized {
			continue
		}
		if i == windowIndex {
			return pos
		}
		pos++
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
func (m *OS) item(icon, label, action string, dim bool) ContextMenuItem {
	return ContextMenuItem{
		Icon:   icon,
		Label:  label,
		Action: action,
		Hint:   contextMenuHint(m.KeybindRegistry, action),
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
	win := m.GetFocusedWindow()

	hasSelection := win != nil && win.SelectedText != ""
	// Pasting reaches the shell only from terminal mode; the clipboard reply is
	// dropped in every other mode. Dimming says so rather than letting the row
	// look live and do nothing.
	canPaste := m.Mode == TerminalMode
	canSplit := m.AutoTiling
	// Renaming is refused outright when titles are hidden, since there would be
	// nowhere for the new name to show up.
	canRename := config.WindowTitlePosition != "hidden"

	closeItem := m.item(glyphClose, "Close pane", "close_window", false)
	closeItem.Warn = true

	return contextMenuWindowName(m, windowIndex, "Pane"), []ContextMenuItem{
		m.item(glyphCopy, "Copy selection", "copy_selection", !hasSelection),
		m.item(glyphPaste, "Paste", "paste_clipboard", !canPaste),
		separator(),
		m.item(glyphSplitV, "Split right", "split_vertical", !canSplit),
		m.item(glyphSplitH, "Split down", "split_horizontal", !canSplit),
		separator(),
		m.item(glyphRename, "Rename", "rename_window", !canRename),
		m.item(glyphZoom, "Zoom", "toggle_zoom", false),
		m.item(glyphMinimize, "Minimize", "minimize_window", false),
		closeItem,
	}
}

// dockItemMenu is the menu for one minimized window in the dock.
//
// Restoring the nth minimized window is only an action for the first nine of
// them, so a tenth entry shows the row dimmed rather than pretending.
func (m *OS) dockItemMenu(windowIndex int) (string, []ContextMenuItem) {
	title := contextMenuWindowName(m, windowIndex, "Window")

	pos := m.minimizedPosition(windowIndex)
	action := ""
	if pos >= 0 && pos < 9 {
		action = "restore_minimized_" + string(rune('1'+pos))
	}

	return title, []ContextMenuItem{
		m.item(glyphRestore, "Restore", action, action == ""),
		m.item(glyphRestore, "Restore all", "restore_all", false),
	}
}

// dockMenu is the menu for the dock away from any of its entries.
func (m *OS) dockMenu() (string, []ContextMenuItem) {
	return "Dock", []ContextMenuItem{
		m.item(glyphNew, "New window", "new_window", false),
		m.item(glyphTiling, "Toggle tiling", "toggle_tiling", false),
		m.item(glyphRestore, "Restore all", "restore_all", !m.HasMinimizedWindows()),
	}
}

// desktopMenu is the menu for empty space: making something appear, and the
// session-wide overlays.
func (m *OS) desktopMenu() (string, []ContextMenuItem) {
	return "Desktop", []ContextMenuItem{
		m.item(glyphNew, "New window", "new_window", false),
		m.item(glyphTiling, "Toggle tiling", "toggle_tiling", false),
		separator(),
		m.item(glyphPalette, "Command palette", "prefix_command_palette", false),
		m.item(glyphSettings, "Settings", "prefix_settings", false),
		m.item(glyphHelp, "Help", "toggle_help", false),
	}
}
