package app

import (
	"fmt"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// RebuildBSPTreeFromPositions rebuilds the BSP tree from current window positions.
// Used after layout loading to sync the tree with the loaded positions without retiling.
func (m *OS) RebuildBSPTreeFromPositions() {
	// Clear existing tree and rebuild from scratch with current window order
	delete(m.WorkspaceTrees, m.CurrentWorkspace)
	tree := m.GetOrCreateBSPTree()
	if tree == nil {
		return
	}

	var visibleWindows []*terminal.Window
	for _, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.IsFloating {
			visibleWindows = append(visibleWindows, w)
		}
	}

	// Re-add all windows to BSP tree
	for _, w := range visibleWindows {
		intID := m.getWindowIntID(w.ID)
		existingIDs := tree.GetAllWindowIDs()
		if len(existingIDs) == 0 {
			tree.InsertWindow(intID, 0, layout.SplitNone, 0.5, m.GetBSPBounds())
		} else {
			tree.InsertWindow(intID, existingIDs[len(existingIDs)-1], layout.SplitNone, 0.5, m.GetBSPBounds())
		}
	}

	// Sync ratios from actual positions
	windowRects := make(map[int]layout.Rect)
	for _, w := range visibleWindows {
		intID := m.getWindowIntID(w.ID)
		windowRects[intID] = layout.Rect{X: w.X, Y: w.Y, W: w.Width, H: w.Height}
	}
	tree.SyncRatiosFromGeometry(windowRects, m.GetBSPBounds())
}

// Layout mode names as they travel in session state. They name the selection
// between tiling layouts only; whether tiling is on at all is AutoTiling, which
// is carried separately and is why disabling tiling does not erase the mode.
const (
	LayoutModeBSP         = "bsp"
	LayoutModeMasterStack = "master-stack"
	LayoutModeScrolling   = "scrolling"
)

// LayoutModeName returns the current layout mode under the name session state
// uses for it.
func (m *OS) LayoutModeName() string {
	switch {
	case m.UseScrollingLayout:
		return LayoutModeScrolling
	case m.UseBSPLayout:
		return LayoutModeBSP
	default:
		return LayoutModeMasterStack
	}
}

// LayoutName returns the layout the user sees, which is the layout mode when
// tiling is on and "floating" when it is off. LayoutModeName deliberately keeps
// reporting the remembered mode while tiling is disabled, so it cannot answer
// this on its own.
func (m *OS) LayoutName() string {
	if !m.AutoTiling {
		return LayoutFloating
	}
	return m.LayoutModeName()
}

// LayoutFloating is the layout name reported when tiling is off.
const LayoutFloating = "floating"

// FireLayoutChanged announces the layout the session ended up in. Layout
// mutations report through this one place rather than building the payload
// themselves, so every one of them names the layout the same way.
func (m *OS) FireLayoutChanged() {
	m.FireHookContext(hooks.AfterLayoutChange, hooks.Context{Layout: m.LayoutName()})
}

// FireResized announces that a window settled at a new size. It takes the
// window rather than reading the focused one so the caller cannot report the
// size of a window other than the one it resized.
func (m *OS) FireResized(w *terminal.Window) {
	if w == nil {
		return
	}
	m.FireHookContext(hooks.AfterResize, hooks.Context{
		WindowID:   w.ID,
		WindowName: w.Title(),
		Workspace:  w.Workspace,
		Width:      w.Width,
		Height:     w.Height,
	})
}

// hookDrainTimeout bounds how long an exiting client waits for its hooks.
const hookDrainTimeout = 2 * time.Second

// FireAttached announces that this client is now driving a session, after its
// windows have been restored so a hook that queries the session sees it whole.
func (m *OS) FireAttached() {
	m.FireHookContext(hooks.AfterAttach, hooks.Context{})
}

// FireDetached announces that this client is leaving. It waits for the hooks it
// just fired, because the caller quits immediately afterwards and hooks run in
// goroutines the process exit would otherwise discard unrun.
func (m *OS) FireDetached() {
	if m.HookManager == nil {
		return
	}
	m.FireHookContext(hooks.AfterDetach, hooks.Context{})
	m.HookManager.WaitTimeout(hookDrainTimeout)
}

// ApplyLayoutModeName sets the layout mode from the name session state carries,
// without retiling or notifying: it is the state-sync half of the Enable*
// functions, and the caller retiles once it has applied the rest of the sync.
//
// An empty or unrecognized name leaves the mode alone. That is what lets the
// field be additive: a daemon or a peer client that never sets it cannot reset
// this client's layout to a default it did not choose.
func (m *OS) ApplyLayoutModeName(name string) {
	switch name {
	case LayoutModeScrolling:
		m.UseScrollingLayout, m.UseBSPLayout = true, false
	case LayoutModeBSP:
		m.UseScrollingLayout, m.UseBSPLayout = false, true
	case LayoutModeMasterStack:
		m.UseScrollingLayout, m.UseBSPLayout = false, false
	}
}

// ToggleLayoutMode cycles through layout modes: BSP -> master-stack -> scrolling -> BSP.
func (m *OS) ToggleLayoutMode() {
	m.resetTiledFlags()
	if m.UseScrollingLayout {
		// scrolling -> BSP
		m.UseScrollingLayout = false
		m.UseBSPLayout = true
		if m.WorkspaceTrees == nil {
			m.WorkspaceTrees = make(map[int]*layout.BSPTree)
		}
		m.WorkspaceTrees[m.CurrentWorkspace] = nil
		m.ShowNotification("Layout: BSP tiling", "info", config.NotificationDuration)
	} else if m.UseBSPLayout {
		// BSP -> master-stack
		m.UseBSPLayout = false
		m.ShowNotification("Layout: master-stack", "info", config.NotificationDuration)
	} else {
		// master-stack -> scrolling
		m.UseScrollingLayout = true
		delete(m.WorkspaceScrollingLayouts, m.CurrentWorkspace)
		m.ShowNotification("Layout: scrolling (niri)", "info", config.NotificationDuration)
	}
	if !m.AutoTiling && (m.UseScrollingLayout || m.UseBSPLayout) {
		m.AutoTiling = true
	}
	if m.AutoTiling {
		m.TileAllWindows()
	}
	m.FireLayoutChanged()
}

// resetTiledFlags clears the Tiled flag on all current workspace windows
// and invalidates caches. Call when switching layout modes to prevent
// stale shared-border state from bleeding between modes.
func (m *OS) resetTiledFlags() {
	for i := range m.Windows {
		if m.Windows[i].Workspace == m.CurrentWorkspace {
			m.Windows[i].Tiled = false
			m.Windows[i].InvalidateCache()
		}
	}
}

// EnableScrollingLayout directly enables scrolling layout mode.
func (m *OS) EnableScrollingLayout() {
	m.resetTiledFlags()
	m.UseScrollingLayout = true
	m.UseBSPLayout = false
	if !m.AutoTiling {
		m.AutoTiling = true
	}
	// Clear old scrolling layout to rebuild from current windows
	delete(m.WorkspaceScrollingLayouts, m.CurrentWorkspace)
	m.TileAllWindows()
	m.ShowNotification("Layout: scrolling (niri)", "info", config.NotificationDuration)
	m.FireLayoutChanged()
}

// EnableBSPLayout directly enables BSP layout mode.
func (m *OS) EnableBSPLayout() {
	m.resetTiledFlags()
	m.UseScrollingLayout = false
	m.UseBSPLayout = true
	if !m.AutoTiling {
		m.AutoTiling = true
	}
	// Clear old BSP tree to rebuild
	if m.WorkspaceTrees == nil {
		m.WorkspaceTrees = make(map[int]*layout.BSPTree)
	}
	m.WorkspaceTrees[m.CurrentWorkspace] = nil
	m.TileAllWindows()
	m.ShowNotification("Layout: BSP tiling", "info", config.NotificationDuration)
	m.FireLayoutChanged()
}

// EnableMasterStackLayout directly enables master-stack layout mode.
func (m *OS) EnableMasterStackLayout() {
	m.resetTiledFlags()
	m.UseScrollingLayout = false
	m.UseBSPLayout = false
	if !m.AutoTiling {
		m.AutoTiling = true
	}
	m.TileAllWindows()
	m.ShowNotification("Layout: master-stack", "info", config.NotificationDuration)
	m.FireLayoutChanged()
}

// DisableAllTiling disables all tiling modes and resets window state.
func (m *OS) DisableAllTiling() {
	m.AutoTiling = false
	m.UseScrollingLayout = false
	m.resetTiledFlags()
	m.ShowNotification("Tiling disabled", "info", config.NotificationDuration)
	m.FireLayoutChanged()
}

// NextLayout cycles to the next saved layout template.
func (m *OS) NextLayout() {
	templates, err := LoadLayoutTemplates()
	if err != nil || len(templates) == 0 {
		m.ShowNotification("No saved layouts", "warn", 0)
		return
	}

	m.LayoutCycleIndex = (m.LayoutCycleIndex + 1) % len(templates)
	tmpl := templates[m.LayoutCycleIndex]
	ApplyLayoutTemplate(tmpl, m)
	m.ShowNotification(fmt.Sprintf("Layout: %s (%d/%d)", tmpl.Name, m.LayoutCycleIndex+1, len(templates)), "info", 0)
}

// PrevLayout cycles to the previous saved layout template.
func (m *OS) PrevLayout() {
	templates, err := LoadLayoutTemplates()
	if err != nil || len(templates) == 0 {
		m.ShowNotification("No saved layouts", "warn", 0)
		return
	}

	m.LayoutCycleIndex--
	if m.LayoutCycleIndex < 0 {
		m.LayoutCycleIndex = len(templates) - 1
	}
	tmpl := templates[m.LayoutCycleIndex]
	ApplyLayoutTemplate(tmpl, m)
	m.ShowNotification(fmt.Sprintf("Layout: %s (%d/%d)", tmpl.Name, m.LayoutCycleIndex+1, len(templates)), "info", 0)
}
