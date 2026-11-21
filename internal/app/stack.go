package app

import (
	"errors"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

var errNoStackPane = errors.New("no pane")

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

func (m *OS) stackTiled(id string) (*terminal.Window, error) {
	w := m.windowByID(id)
	if w == nil {
		return nil, errfmt.Errorf("%w %s", errNoStackPane, id)
	}
	if w.Workspace != m.CurrentWorkspace {
		return nil, errfmt.Errorf("pane %s is on workspace %d, not on the workspace on screen", id, w.Workspace)
	}
	if w.IsFloating || w.IsPopup || w.IsScratch {
		return nil, errfmt.Errorf("pane %s floats; only tiled panes stack", id)
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
		return errfmt.Errorf("pane %s is in another stack; take it out first", b)
	}
	if wb.Minimized {
		return errfmt.Errorf("pane %s is minimized", b)
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
	open := m.stackOpen(wa.Stack, wa.Workspace)
	if open == nil {
		return errfmt.Errorf("the stack of %s has no open pane", a)
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

func (m *OS) stackOpen(key string, workspace int) *terminal.Window {
	for _, w := range m.stackMembers(key, workspace) {
		if !w.Minimized {
			return w
		}
	}
	return nil
}

func (m *OS) collapse(w *terminal.Window) {
	w.PreMinimizeX, w.PreMinimizeY = w.X, w.Y
	w.PreMinimizeWidth, w.PreMinimizeHeight = w.Width, w.Height
	w.Minimized = true
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
		return errfmt.Errorf("%w %s", errNoStackPane, id)
	}
	if w.Stack == "" {
		return errfmt.Errorf("pane %s is not in a stack", id)
	}
	if !w.Minimized {
		_ = m.FocusWindowByID(id)
		m.finishTreeEdit()
		return nil
	}
	open := m.stackOpen(w.Stack, w.Workspace)
	if open == nil || !tree.HasWindow(m.GetWindowIntID(open.ID)) {
		return errfmt.Errorf("the stack of %s has no open pane", id)
	}
	if !tree.ReplaceWindow(m.GetWindowIntID(open.ID), m.GetWindowIntID(w.ID)) {
		return errfmt.Errorf("the stack of %s has no tile", id)
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
		return errfmt.Errorf("%w %s", errNoStackPane, id)
	}
	if w.Stack == "" {
		return errfmt.Errorf("pane %s is not in a stack", id)
	}
	if !w.Minimized && !m.handOverStack(w, tree) {
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

func (m *OS) handOverStack(w *terminal.Window, tree *layout.BSPTree) bool {
	if w.Stack == "" || tree == nil {
		return false
	}
	members := m.stackMembers(w.Stack, w.Workspace)
	at := slices.Index(members, w)
	var next *terminal.Window
	for i := 1; i < len(members); i++ {
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
			rest[0].Minimized = false
		}
	}
}

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
