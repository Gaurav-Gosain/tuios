package app

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Stacks: several panes share one tile of the split tree. One of them is open
// and has the tile; each of the others shows as a one-row title above or below
// it, in stack order, and is minimized. Opening another member trades places
// with the open one inside the same tile, so the split tree never changes
// shape. This is Zellij's stacked panes, and the GUI's stacks (plan 2.8).
//
// A stack is the windows that carry the same Stack key, ordered by StackIndex.
// Both travel with the window state, so every client draws the same stack and
// lays out the same rows. The open member is the one that is not minimized.
//
// A member that leaves the stack by any other road (restored from the dock,
// closed, floated) leaves it cleanly: an open member that goes hands its tile
// to the next member first, so the stack keeps its place in the tree.

var errNoStackPane = errors.New("no pane")

// stackMembers returns the windows of a stack on its workspace, in order.
func (m *OS) stackMembers(key string, workspace int) []*terminal.Window {
	if key == "" {
		return nil
	}
	var v []*terminal.Window
	for _, w := range m.Windows {
		if w != nil && w.Stack == key && w.Workspace == workspace {
			v = append(v, w)
		}
	}
	slices.SortStableFunc(v, func(a, b *terminal.Window) int { return a.StackIndex - b.StackIndex })
	return v
}

// stackRows returns how many title rows a stack's open member gives up above
// it and below it: one per member before it and one per member after it.
func (m *OS) stackRows(w *terminal.Window) (above, below int) {
	if w == nil || w.Stack == "" || w.Minimized || m.LayoutName() != LayoutModeBSP {
		return 0, 0
	}
	members := m.stackMembers(w.Stack, w.Workspace)
	at := slices.Index(members, w)
	if at < 0 || len(members) < 2 {
		return 0, 0
	}
	return at, len(members) - 1 - at
}

// stackTiled returns the tiled window id on the workspace on screen, or an
// error that says why it cannot join a stack.
func (m *OS) stackTiled(id string) (*terminal.Window, error) {
	w := m.windowByID(id)
	if w == nil {
		return nil, fmt.Errorf("%w %s", errNoStackPane, id)
	}
	if w.Workspace != m.CurrentWorkspace {
		return nil, fmt.Errorf("pane %s is on workspace %d, not on the workspace on screen", id, w.Workspace)
	}
	if w.IsFloating || w.IsPopup || w.IsScratch {
		return nil, fmt.Errorf("pane %s floats; only tiled panes stack", id)
	}
	return w, nil
}

// StackPanes puts the pane b into the stack of the pane a, after its last
// member. A pane on its own starts a stack. The open member stays open; b
// becomes a title row. Focus stays where it is.
func (m *OS) StackPanes(a, b string) error {
	tree, err := m.treeEditTarget()
	if err != nil {
		return err
	}
	if a == b {
		return errors.New("a pane cannot stack with itself")
	}
	wa, err := m.stackTiled(a)
	if err != nil {
		return err
	}
	wb, err := m.stackTiled(b)
	if err != nil {
		return err
	}
	if wb.Stack != "" && wb.Stack == wa.Stack {
		return nil
	}
	if wb.Stack != "" {
		return fmt.Errorf("pane %s is in another stack; take it out first", b)
	}
	if wb.Minimized {
		return fmt.Errorf("pane %s is minimized", b)
	}
	if wa.Stack == "" {
		wa.Stack = wa.ID
		wa.StackIndex = 0
	}
	members := m.stackMembers(wa.Stack, wa.Workspace)
	next := 0
	for _, x := range members {
		next = max(next, x.StackIndex+1)
	}
	// The open member keeps the tile. If b is the open one of the two (a is
	// a title row), b takes a's place in the order and a stays where it is.
	open := m.stackOpen(wa.Stack, wa.Workspace)
	if open == nil {
		return fmt.Errorf("the stack of %s has no open pane", a)
	}
	focused := m.GetFocusedWindow()
	tree.RemoveWindow(m.GetWindowIntID(wb.ID))
	wb.Stack, wb.StackIndex = wa.Stack, next
	m.collapse(wb)
	if focused == wb {
		_ = m.FocusWindowByID(open.ID)
	}
	m.finishTreeEdit()
	return nil
}

// stackOpen returns the open member of a stack.
func (m *OS) stackOpen(key string, workspace int) *terminal.Window {
	for _, w := range m.stackMembers(key, workspace) {
		if !w.Minimized {
			return w
		}
	}
	return nil
}

// collapse makes a stack member a title row: minimized, off the dock's
// highlight, at the size its open neighbour has, so its program keeps a
// sensible screen.
func (m *OS) collapse(w *terminal.Window) {
	w.PreMinimizeX, w.PreMinimizeY = w.X, w.Y
	w.PreMinimizeWidth, w.PreMinimizeHeight = w.Width, w.Height
	w.Minimized = true
	w.MinimizeOrder = time.Now().UnixNano()
	w.InvalidateCache()
}

// OpenStacked opens a title row of a stack: the pane takes the stack's tile
// and the pane that had it becomes a title row. Focus goes to the pane.
func (m *OS) OpenStacked(id string) error {
	tree, err := m.treeEditTarget()
	if err != nil {
		return err
	}
	w := m.windowByID(id)
	if w == nil {
		return fmt.Errorf("%w %s", errNoStackPane, id)
	}
	if w.Stack == "" {
		return fmt.Errorf("pane %s is not in a stack", id)
	}
	if !w.Minimized {
		_ = m.FocusWindowByID(id)
		m.finishTreeEdit()
		return nil
	}
	open := m.stackOpen(w.Stack, w.Workspace)
	if open == nil || !tree.HasWindow(m.GetWindowIntID(open.ID)) {
		return fmt.Errorf("the stack of %s has no open pane", id)
	}
	if !tree.ReplaceWindow(m.GetWindowIntID(open.ID), m.GetWindowIntID(w.ID)) {
		return fmt.Errorf("the stack of %s has no tile", id)
	}
	m.collapse(open)
	w.Minimized = false
	_ = m.FocusWindowByID(id)
	m.finishTreeEdit()
	return nil
}

// Unstack takes the pane id out of its stack and puts it beside the stack,
// in a tile of its own. The open member stays open; when id is the open
// member, the next member opens in its place first.
func (m *OS) Unstack(id string) error {
	tree, err := m.treeEditTarget()
	if err != nil {
		return err
	}
	w := m.windowByID(id)
	if w == nil {
		return fmt.Errorf("%w %s", errNoStackPane, id)
	}
	if w.Stack == "" {
		return fmt.Errorf("pane %s is not in a stack", id)
	}
	if !w.Minimized && !m.handOverStack(w, tree) {
		// The only member: it already has a tile of its own.
		m.leaveStack(w)
		m.finishTreeEdit()
		return nil
	}
	open := m.stackOpen(w.Stack, w.Workspace)
	m.leaveStack(w)
	w.Minimized = false
	n := m.GetWindowIntID(w.ID)
	target := 0
	if open != nil {
		target = m.GetWindowIntID(open.ID)
	}
	tree.InsertWindowWithPreselection(n, target, layout.PreselectionDown, m.GetBSPBounds(), m.separatorGap())
	_ = m.FocusWindowByID(id)
	m.finishTreeEdit()
	return nil
}

// handOverStack gives the tile of w, the open member of a stack, to the next
// member and makes w a pane of its own, out of the stack and out of the tree.
// It reports whether there was a member to hand over to.
func (m *OS) handOverStack(w *terminal.Window, tree *layout.BSPTree) bool {
	if w.Stack == "" || tree == nil {
		return false
	}
	members := m.stackMembers(w.Stack, w.Workspace)
	at := slices.Index(members, w)
	var next *terminal.Window
	for i := 1; i < len(members); i++ {
		// The member after it, else the one before.
		for _, j := range []int{at + i, at - i} {
			if j >= 0 && j < len(members) && members[j].Minimized && next == nil {
				next = members[j]
			}
		}
	}
	if next == nil || !tree.ReplaceWindow(m.GetWindowIntID(w.ID), m.GetWindowIntID(next.ID)) {
		return false
	}
	next.Minimized = false
	m.leaveStack(w)
	return true
}

// leaveStack takes w out of its stack. A stack of one is no stack.
func (m *OS) leaveStack(w *terminal.Window) {
	key, ws := w.Stack, w.Workspace
	if key == "" {
		return
	}
	w.Stack, w.StackIndex = "", 0
	rest := m.stackMembers(key, ws)
	if len(rest) == 1 {
		rest[0].Stack, rest[0].StackIndex = "", 0
		if rest[0].Minimized {
			// A lone title row would have no tile: it opens.
			rest[0].Minimized = false
		}
	}
}

// openOrphanStacks opens the first title row of any stack on the workspace
// on screen whose open member went away by a road that did not hand its tile
// over. The tiler then gives it a tile like any pane that has none.
func (m *OS) openOrphanStacks() {
	seen := map[string]bool{}
	for _, w := range m.Windows {
		if w == nil || w.Stack == "" || w.Workspace != m.CurrentWorkspace || seen[w.Stack] {
			continue
		}
		seen[w.Stack] = true
		members := m.stackMembers(w.Stack, w.Workspace)
		if m.stackOpen(w.Stack, w.Workspace) == nil && len(members) > 0 {
			members[0].Minimized = false
		}
		if len(members) == 1 {
			members[0].Stack, members[0].StackIndex = "", 0
		}
	}
}

// renderStackRows draws the title rows of every stack on the workspace on
// screen, in the rows the open member gave up for them.
func (m *OS) renderStackRows() []*lipgloss.Layer {
	if m.LayoutName() != LayoutModeBSP {
		return nil
	}
	var layers []*lipgloss.Layer
	for _, open := range m.Windows {
		if open == nil || open.Stack == "" || open.Minimized || open.Workspace != m.CurrentWorkspace {
			continue
		}
		members := m.stackMembers(open.Stack, open.Workspace)
		at := slices.Index(members, open)
		if at < 0 || len(members) < 2 || open.Width < 4 {
			continue
		}
		style := lipgloss.NewStyle().Foreground(theme.BorderUnfocusedOn(m.host.bg))
		for i, w := range members {
			if i == at {
				continue
			}
			y := open.Y - (at - i)
			if i > at {
				y = open.Y + open.Height + (i - at - 1)
			}
			label := w.CustomName
			if label == "" {
				label = w.Title()
			}
			text := " ▸ " + truncateRunes(label, max(open.Width-4, 1))
			if pad := open.Width - lipgloss.Width(text); pad > 0 {
				text += strings.Repeat(" ", pad)
			}
			layers = append(layers, lipgloss.NewLayer(style.Render(text)).X(open.X).Y(y).Z(1).ID("stack-row-"+w.ID))
		}
	}
	return layers
}

// FocusPane focuses a pane. A stack's title row opens in the stack's tile
// instead, so focusing it from a list does not take it out of the stack.
func (m *OS) FocusPane(id string) error {
	if w := m.windowByID(id); w != nil && w.Stack != "" && w.Minimized && w.Workspace == m.CurrentWorkspace {
		if err := m.OpenStacked(id); err == nil {
			return nil
		}
	}
	return m.FocusWindowByID(id)
}
