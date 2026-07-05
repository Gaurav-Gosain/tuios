package app

import (
	"fmt"

	"github.com/Gaurav-Gosain/tuios/internal/config"
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
}

// DisableAllTiling disables all tiling modes and resets window state.
func (m *OS) DisableAllTiling() {
	m.AutoTiling = false
	m.UseScrollingLayout = false
	m.resetTiledFlags()
	m.ShowNotification("Tiling disabled", "info", config.NotificationDuration)
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
