package app

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// RebuildBSPTreeFromPositions rebuilds the BSP tree from current window positions.
// Used after layout loading to sync the tree with the loaded positions without retiling.
func (m *OS) RebuildBSPTreeFromPositions() {
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

	for _, w := range visibleWindows {
		intID := m.GetWindowIntID(w.ID)
		existingIDs := tree.GetAllWindowIDs()
		if len(existingIDs) == 0 {
			tree.InsertWindow(intID, 0, layout.SplitNone, 0.5, m.GetBSPBounds(), m.separatorGap())
		} else {
			tree.InsertWindow(intID, existingIDs[len(existingIDs)-1], layout.SplitNone, 0.5, m.GetBSPBounds(), m.separatorGap())
		}
	}

	windowRects := make(map[int]layout.Rect)
	for _, w := range visibleWindows {
		intID := m.GetWindowIntID(w.ID)
		windowRects[intID] = layout.Rect{X: w.X, Y: w.Y, W: w.Width, H: w.Height}
	}
	tree.SyncRatiosFromGeometry(windowRects, m.GetBSPBounds(), m.separatorGap())
}

// Layout mode names as they travel in session state. They name the selection
// between tiling layouts only; whether tiling is on at all is AutoTiling, which
// is carried separately and is why disabling tiling does not erase the mode.
// They are the config package's spellings rather than a second set: the option
// registry publishes the accepted set for [startup] layout and cannot import
// this package, so the names have to live where both can reach them.
const (
	LayoutModeBSP         = config.LayoutModeBSP
	LayoutModeMasterStack = config.LayoutModeMasterStack
	LayoutModeScrolling   = config.LayoutModeScrolling
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

const hookDrainTimeout = 2 * time.Second

// FireAttached announces that this client is now driving a session, after its
// windows have been restored so a hook that queries the session sees it whole.
func (m *OS) FireAttached() {
	m.FireHookContext(hooks.AfterAttach, hooks.Context{})
}

// FireDetached announces that this client is leaving. It waits for the hooks it
// just fired, because the caller quits immediately afterwards and hooks run in
// goroutines the process exit would otherwise discard unrun.
//
// It fires once per client. A client can arrive here twice, because a
// deliberate detach fires it and the teardown that follows fires it again for
// the clients that have no deliberate detach, and one person leaving once is
// one detach.
func (m *OS) FireDetached() {
	if !m.detachFired.CompareAndSwap(false, true) {
		return
	}
	m.reportFocusLost()
	if m.HookManager == nil {
		return
	}
	m.FireHookContext(hooks.AfterDetach, hooks.Context{})
	m.HookManager.WaitTimeout(hookDrainTimeout)
}

func (m *OS) recordWorkspaceLayoutMode() {
	if session.IsScratchWorkspace(m.CurrentWorkspace) {
		return
	}
	if m.WorkspaceLayoutMode == nil {
		m.WorkspaceLayoutMode = make(map[int]string)
	}
	m.WorkspaceLayoutMode[m.CurrentWorkspace] = m.LayoutModeName()
}

// ApplyLayoutModeName sets the layout mode from the name session state carries,
// without retiling or notifying: it is the state-sync half of the Enable*
// functions, and the caller retiles once it has applied the rest of the sync.
// It records nothing: the workspace map is written by the rows that choose a
// layout, and adopting a mode here would pin it to whatever workspace happens
// to be on screen.
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

func (m *OS) adoptSessionLayoutMode(state *session.SessionState) {
	if state == nil || state.LayoutMode == "" {
		return
	}
	ws := clampWorkspace(state.CurrentWorkspace)
	if _, ok := m.WorkspaceLayoutMode[ws]; ok {
		return
	}
	m.sessionLayoutMode = state.LayoutMode
	if m.WorkspaceLayoutMode == nil {
		m.WorkspaceLayoutMode = make(map[int]string)
	}
	for i := 1; i <= state.NumWorkspaces; i++ {
		if _, ok := m.WorkspaceLayoutMode[i]; !ok {
			m.WorkspaceLayoutMode[i] = state.LayoutMode
		}
	}
}

// ToggleLayoutMode cycles through layout modes: BSP -> master-stack -> scrolling -> BSP.
func (m *OS) ToggleLayoutMode() {
	m.settleSizes(func() { m.toggleLayoutMode() })
}

func (m *OS) toggleLayoutMode() {
	m.resetTiledFlags()
	if m.UseScrollingLayout {
		m.UseScrollingLayout = false
		m.UseBSPLayout = true
		if m.WorkspaceTrees == nil {
			m.WorkspaceTrees = make(map[int]*layout.BSPTree)
		}
		m.WorkspaceTrees[m.CurrentWorkspace] = nil
		m.ShowNotification("Layout: BSP tiling", "info", m.Settings.NotificationDuration)
	} else if m.UseBSPLayout {
		m.UseBSPLayout = false
		m.ShowNotification("Layout: master-stack", "info", m.Settings.NotificationDuration)
	} else {
		m.UseScrollingLayout = true
		delete(m.WorkspaceScrollingLayouts, m.CurrentWorkspace)
		m.ShowNotification("Layout: scrolling (niri)", "info", m.Settings.NotificationDuration)
	}
	if !m.AutoTiling && (m.UseScrollingLayout || m.UseBSPLayout) {
		m.AutoTiling = true
	}
	m.recordWorkspaceLayoutMode()
	if m.AutoTiling {
		m.TileAllWindows()
	}
	m.FireLayoutChanged()
}

func (m *OS) resetTiledFlags() {
	m.requireRealLayout()

	for i := range m.Windows {
		if m.Windows[i].Workspace == m.CurrentWorkspace {
			m.Windows[i].InvalidateCache()
		}
	}
}

// EnableScrollingLayout directly enables scrolling layout mode.
func (m *OS) EnableScrollingLayout() {
	m.settleSizes(func() { m.enableScrollingLayout() })
}

func (m *OS) enableScrollingLayout() {
	m.resetTiledFlags()
	m.UseScrollingLayout = true
	m.UseBSPLayout = false
	if !m.AutoTiling {
		m.AutoTiling = true
	}
	delete(m.WorkspaceScrollingLayouts, m.CurrentWorkspace)
	m.recordWorkspaceLayoutMode()
	m.TileAllWindows()
	m.ShowNotification("Layout: scrolling (niri)", "info", m.Settings.NotificationDuration)
	m.FireLayoutChanged()
}

// EnableBSPLayout directly enables BSP layout mode.
func (m *OS) EnableBSPLayout() {
	m.settleSizes(func() { m.enableBSPLayout() })
}

func (m *OS) enableBSPLayout() {
	m.resetTiledFlags()
	m.UseScrollingLayout = false
	m.UseBSPLayout = true
	if !m.AutoTiling {
		m.AutoTiling = true
	}
	if m.WorkspaceTrees == nil {
		m.WorkspaceTrees = make(map[int]*layout.BSPTree)
	}
	m.WorkspaceTrees[m.CurrentWorkspace] = nil
	m.recordWorkspaceLayoutMode()
	m.TileAllWindows()
	m.ShowNotification("Layout: BSP tiling", "info", m.Settings.NotificationDuration)
	m.FireLayoutChanged()
}

// EnableMasterStackLayout directly enables master-stack layout mode.
func (m *OS) EnableMasterStackLayout() {
	m.settleSizes(func() { m.enableMasterStackLayout() })
}

func (m *OS) enableMasterStackLayout() {
	m.resetTiledFlags()
	m.UseScrollingLayout = false
	m.UseBSPLayout = false
	if !m.AutoTiling {
		m.AutoTiling = true
	}
	m.recordWorkspaceLayoutMode()
	m.TileAllWindows()
	m.ShowNotification("Layout: master-stack", "info", m.Settings.NotificationDuration)
	m.FireLayoutChanged()
}

// DisableAllTiling turns tiling off. The layout mode is remembered, so turning
// tiling back on, or reattaching, returns to the layout the session had.
func (m *OS) DisableAllTiling() {
	wasOn := m.AutoTiling
	m.SetAutoTiling(false)
	if !wasOn {
		m.FireLayoutChanged()
	}
	m.ShowNotification("Tiling off", "info", m.Settings.NotificationDuration)
}

// NextLayout cycles to the next saved layout template.
func (m *OS) NextLayout() tea.Cmd { return m.cycleLayout(1) }

// PrevLayout cycles to the previous saved layout template.
func (m *OS) PrevLayout() tea.Cmd { return m.cycleLayout(-1) }

func (m *OS) cycleLayout(step int) tea.Cmd {
	return m.withLayoutTemplates(func(templates []LayoutTemplate) tea.Cmd {
		if len(templates) == 0 {
			m.ShowNotification("No saved layouts", "warn", 0)
			return nil
		}
		n := len(templates)
		m.LayoutCycleIndex = ((m.LayoutCycleIndex+step)%n + n) % n
		tmpl := templates[m.LayoutCycleIndex]
		ApplyLayoutTemplate(tmpl, m)
		m.ShowNotification(fmt.Sprintf("Layout: %s (%d/%d)", tmpl.Name, m.LayoutCycleIndex+1, n), "info", 0)
		return nil
	})
}
