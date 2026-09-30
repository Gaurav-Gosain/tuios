package app

import (
	"errors"
	"fmt"
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The two tape commands tuios xpanes routes to the attached client, and that
// anyone can run with tuios run-command: ArrangePanes and SetMultifocus. Both
// act on state only the client holds: the BSP tree of a workspace and the
// multifocus set live here, not in the daemon.
//
// Both take window ids. A window the daemon just created reaches this client
// as a state push, on a different channel from the routed command, so the
// command can arrive first. An id this client does not know yet is then the
// error errWindowNotHereYet, and the caller tries again.

// ErrWindowNotHereYet starts the error for a window this client has not
// heard of yet. tuios xpanes matches it to retry.
const ErrWindowNotHereYet = "is not in this client yet"

// lookUpWindows resolves each target to a window id, in order.
func (m *OS) lookUpWindows(targets []string) ([]string, error) {
	ids := make([]string, 0, len(targets))
	for _, t := range targets {
		id, err := m.resolveWindowTarget(t)
		if err != nil {
			return nil, fmt.Errorf("window %s %s: %w", t, ErrWindowNotHereYet, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// ArrangePanesExec lays the tiled panes of the current workspace out again as
// tiled, even-horizontal or even-vertical. The named windows come first, in
// the order given, and the other panes of the workspace follow in window
// order. The workspace's split ratios are replaced.
func (m *OS) ArrangePanesExec(kind string, windows []string) error {
	if !m.AutoTiling {
		return errTilingOff
	}
	if mode := m.LayoutModeName(); mode != config.LayoutModeBSP {
		return fmt.Errorf("ArrangePanes needs the bsp layout, and this session uses %s", mode)
	}
	first, err := m.lookUpWindows(windows)
	if err != nil {
		return err
	}
	order := first
	for _, w := range m.Windows {
		if !slices.Contains(order, w.ID) {
			order = append(order, w.ID)
		}
	}
	var ids []int
	for _, id := range order {
		w := m.windowByID(id)
		if w == nil || w.Workspace != m.CurrentWorkspace || w.Minimized || w.IsFloating {
			continue
		}
		ids = append(ids, m.GetWindowIntID(w.ID))
	}
	if len(ids) == 0 {
		return errors.New("the workspace has no tiled panes to arrange")
	}
	if err := m.GetOrCreateBSPTree().ArrangeTree(kind, ids); err != nil {
		return err
	}
	m.ApplyBSPLayout()
	m.MarkAllDirty()
	return nil
}

// SetMultifocusExec makes the multifocus set exactly the named windows. With
// none it clears the set. A popup cannot join, as with the toggle.
func (m *OS) SetMultifocusExec(windows []string) error {
	ids, err := m.lookUpWindows(windows)
	if err != nil {
		return err
	}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		if w := m.windowByID(id); w != nil && w.IsPopup {
			return fmt.Errorf("window %s is a popup, and a popup cannot join multifocus", id)
		}
		set[id] = true
	}
	for _, w := range m.Windows {
		if m.MultifocusSet[w.ID] || set[w.ID] {
			w.InvalidateCache()
		}
	}
	if len(set) == 0 {
		m.MultifocusSet = nil
		m.ShowNotification("Multifocus: cleared", "info", m.Settings.NotificationDuration)
		return nil
	}
	m.MultifocusSet = set
	m.showMultifocusCount()
	return nil
}
