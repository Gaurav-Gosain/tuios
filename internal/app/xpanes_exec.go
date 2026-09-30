package app

import (
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The two tape commands tuios xpanes routes to the attached client, and that
// anyone can run with tuios run-command: ArrangePanes and SetMultifocus. Both
// act on state only the client holds: the BSP tree of a workspace and the
// multifocus set live here, not in the daemon.
//
// Both take window ids. A window the daemon just created, and the switch to
// its workspace, reach this client as state pushes, on a different channel
// from the routed command, so the command can arrive first. The error then
// holds ErrNotHereYet, and the caller tries again.

// ErrNotHereYet is in the error for a window, or a workspace switch, this
// client has not heard of yet. tuios xpanes matches it to retry.
const ErrNotHereYet = "is not in this client yet"

// lookUpWindows resolves each target to a window, in order. Each must be on
// the current workspace and not minimized: the commands act on what is on
// screen, and a window elsewhere may still be on its way here.
func (m *OS) lookUpWindows(targets []string) ([]*terminal.Window, error) {
	out := make([]*terminal.Window, 0, len(targets))
	for _, t := range targets {
		id, err := m.resolveWindowTarget(t)
		if err != nil {
			return nil, fmt.Errorf("window %s %s: %w", t, ErrNotHereYet, err)
		}
		w := m.windowByID(id)
		switch {
		case w == nil:
			return nil, fmt.Errorf("window %s %s", t, ErrNotHereYet)
		case w.Workspace != m.CurrentWorkspace:
			return nil, fmt.Errorf("window %s is on workspace %d, and workspace %d is showing. The window %s on the showing workspace", t, w.Workspace, m.CurrentWorkspace, ErrNotHereYet)
		case w.Minimized:
			return nil, fmt.Errorf("window %s is minimized", t)
		}
		out = append(out, w)
	}
	return out, nil
}

// ArrangePanesExec lays out the tiled panes of a workspace again as tiled,
// even-horizontal or even-vertical. args is [workspace [window...]]. The
// workspace must be the one showing, so a person who switched away is not
// given a new layout on the workspace they switched to. The named windows
// come first, in the order given, and the other panes of the workspace follow
// in window order. The workspace's split ratios are replaced.
func (m *OS) ArrangePanesExec(kind string, args []string) error {
	if !m.AutoTiling {
		return errTilingOff
	}
	if mode := m.LayoutModeName(); mode != config.LayoutModeBSP {
		return fmt.Errorf("ArrangePanes needs the bsp layout, and this session uses %s", mode)
	}
	if len(args) > 0 {
		ws, err := strconv.Atoi(args[0])
		if err != nil || ws < 1 {
			return fmt.Errorf("ArrangePanes takes a workspace number before the windows, not %q", args[0])
		}
		if ws != m.CurrentWorkspace {
			return fmt.Errorf("workspace %d is not showing: the switch to it %s, or you switched away", ws, ErrNotHereYet)
		}
		args = args[1:]
	}
	first, err := m.lookUpWindows(args)
	if err != nil {
		return err
	}
	order := first
	for _, w := range m.Windows {
		if !slices.Contains(order, w) {
			order = append(order, w)
		}
	}
	var ids []int
	for _, w := range order {
		if w.Workspace != m.CurrentWorkspace || w.Minimized || w.IsFloating {
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

// SetMultifocusExec makes the multifocus set exactly the named windows. Each
// must be on the showing workspace and not minimized. With none it clears the
// set. A popup cannot join, as with the toggle.
func (m *OS) SetMultifocusExec(windows []string) error {
	named, err := m.lookUpWindows(windows)
	if err != nil {
		return err
	}
	set := make(map[string]bool, len(named))
	for _, w := range named {
		if w.IsPopup {
			return fmt.Errorf("window %s is a popup, and a popup cannot join multifocus", w.ID)
		}
		set[w.ID] = true
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
