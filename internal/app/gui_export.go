package app

import (
	"fmt"
	"slices"
	"sort"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// What the GUI bridge (internal/guibridge) reads off the model for a native
// renderer that draws the views itself: the palette's rows, the multifocus
// group, the pinned pane and the scrolling strip. The scratch box is
// ScratchBox in scratch.go.

// BridgePaletteItems is the command palette as a client with no screen of its
// own offers it: the static commands, the user's command keys and the
// plugins' actions, with the same rows left out as the terminal client leaves
// out. The rows the palette builds from the session (panes, sessions, the
// keybind and settings rows) are not here: the renderer builds its own.
func (m *OS) BridgePaletteItems() []CommandPaletteItem {
	items := GetCommandPaletteItems(&m.Settings)
	if !m.agentsSeen() {
		items = slices.DeleteFunc(items, func(it CommandPaletteItem) bool { return it.Category == paletteCategoryAgents })
	}
	if !m.reviewSupported() {
		items = slices.DeleteFunc(items, func(it CommandPaletteItem) bool { return it.Name == paletteReviewName })
	}
	if !m.agentsPageAvailable() {
		items = slices.DeleteFunc(items, func(it CommandPaletteItem) bool { return it.Name == paletteAgentsSettingsName })
	}
	if len(m.MultifocusSet) == 0 {
		items = slices.DeleteFunc(items, func(it CommandPaletteItem) bool { return it.Name == paletteMultiCopyName })
	}
	items = append(items, m.commandPaletteItems()...)
	return append(items, m.pluginPaletteItems()...)
}

// PaletteCategoryCommands and PaletteCategoryPlugins name the palette
// sections of the user's command keys and the plugins' actions.
const (
	PaletteCategoryCommands = paletteCategoryCommands
	PaletteCategoryPlugins  = paletteCategoryPlugins
)

// MultifocusIDs returns the panes that take typing together, sorted. Empty
// when multifocus is off.
func (m *OS) MultifocusIDs() []string {
	ids := make([]string, 0, len(m.MultifocusSet))
	for id, on := range m.MultifocusSet {
		if on && m.windowByID(id) != nil {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// SetMultifocus puts the pane id in the multifocus group or takes it out.
func (m *OS) SetMultifocus(id string, on bool) error {
	w := m.windowByID(id)
	if w == nil {
		return fmt.Errorf("no pane %s", id)
	}
	if m.MultifocusSet[id] == on {
		return nil
	}
	if on && w.IsPopup {
		return fmt.Errorf("a popup cannot join the group")
	}
	for i, x := range m.Windows {
		if x == w {
			m.ToggleMultifocus(i)
			return nil
		}
	}
	return fmt.Errorf("no pane %s", id)
}

// PiPWindow is the pane pinned as this client's picture in picture, and the
// corner the config puts it in. Empty when nothing is pinned.
func (m *OS) PiPWindow() (id, corner string) {
	if m.pip.windowID == "" || m.windowByID(m.pip.windowID) == nil {
		return "", ""
	}
	corner = config.PiPCornerBottomRight
	if m.UserConfig != nil && m.UserConfig.PiP.Corner != "" {
		corner = m.UserConfig.PiP.Corner
	}
	return m.pip.windowID, corner
}

// StripPane is one pane of the scrolling strip, in strip cells: X counts from
// the strip's left end, not from the screen.
type StripPane struct {
	ID         string
	X, Y, W, H int
}

// ScrollStrip is the scrolling layout of the workspace on screen as it will
// settle: the viewport's offset into the strip, the strip's width, and each
// pane's place on it. A renderer that slides the strip itself reads this
// instead of the panes' boxes, which move through the slide. ok is false
// outside the scrolling layout.
func (m *OS) ScrollStrip() (viewport, width int, panes []StripPane, ok bool) {
	if m.LayoutName() != LayoutModeScrolling {
		return 0, 0, nil, false
	}
	sl := m.WorkspaceScrollingLayouts[m.CurrentWorkspace]
	if sl == nil {
		return 0, 0, nil, false
	}
	viewW := m.ScrollingViewWidth()
	left := m.PaneLeft()
	for id, r := range sl.ComputePositions(viewW, m.PaneHeight(), m.PaneTop()) {
		w := m.GetWindowByIntID(id)
		if w == nil || w.Workspace != m.CurrentWorkspace || w.Minimized || w.IsFloating {
			continue
		}
		// ComputePositions works on screen: put the viewport back.
		panes = append(panes, StripPane{ID: w.ID, X: r.X + sl.ViewportX + left, Y: r.Y, W: r.W, H: r.H})
	}
	sort.Slice(panes, func(i, j int) bool {
		if panes[i].X != panes[j].X {
			return panes[i].X < panes[j].X
		}
		return panes[i].Y < panes[j].Y
	})
	return sl.ViewportX, sl.TotalStripWidth(viewW), panes, true
}
