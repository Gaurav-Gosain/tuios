package app

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

var dockEditorSides = []string{"left", "center", "right"}

type dockRowKind int

const (
	dockRowHeader dockRowKind = iota
	dockRowComponent
	dockRowEmpty
	dockRowAvailable
)

type dockEditorRow struct {
	Kind   dockRowKind
	Side   string
	Name   string
	Fixed  string
	Custom bool
}

// OpenDockEditor shows the dock layout editor, remembering the current lists so
// cancel can restore them.
func (m *OS) OpenDockEditor() {
	if m.UserConfig == nil {
		m.UserConfig = config.DefaultConfig()
		m.ConfigReadOnly = true
	}
	m.ShowDockEditor = true
	m.dockList.Scroll = 0
	m.DockEditorOriginal = m.dockLists()

	m.dockList.Cursor = 0
	for i, row := range m.dockEditorRows() {
		if row.Kind == dockRowComponent {
			m.dockList.Cursor = i
			break
		}
	}

	if m.ShowSettings {
		so := m.overlayOffset("settings")
		m.setOverlayOffset("dockeditor", so[0]+8, so[1]+2)
	}
}

// CloseDockEditor hides the editor, keeping the lists as they are.
func (m *OS) CloseDockEditor() {
	m.ShowDockEditor = false
}

// DockEditorRevert puts the lists back as they were when the editor opened.
//
// An undo rather than what Esc does. Every edit here is applied and saved as it
// is made, the way a settings row is, so Esc closing and throwing the session's
// work away would be the surprise: the bar has been showing the new layout for
// as long as the person has been looking at it. This is the way back for
// someone who has decided against it, and it leaves an untouched list untouched
// rather than writing today's defaults into a file that named none.
func (m *OS) DockEditorRevert() tea.Cmd {
	if m.dockLists().equal(m.DockEditorOriginal) {
		return nil
	}
	m.setDockLists(m.DockEditorOriginal)
	return m.commitDockLists(true)
}

type dockLists struct{ Left, Center, Right *[]string }

func (m *OS) dockLists() dockLists {
	if m.UserConfig == nil {
		return dockLists{}
	}
	clone := func(src *[]string) *[]string {
		if src == nil {
			return nil
		}
		out := slices.Clone(*src)
		return &out
	}
	return dockLists{
		Left:   clone(m.UserConfig.Dock.Left),
		Center: clone(m.UserConfig.Dock.Center),
		Right:  clone(m.UserConfig.Dock.Right),
	}
}

func (l dockLists) equal(other dockLists) bool {
	same := func(a, b *[]string) bool {
		if a == nil || b == nil {
			return a == nil && b == nil
		}
		return slices.Equal(*a, *b)
	}
	return same(l.Left, other.Left) &&
		same(l.Center, other.Center) &&
		same(l.Right, other.Right)
}

func (m *OS) setDockLists(l dockLists) {
	if m.UserConfig == nil {
		return
	}
	m.UserConfig.Dock.Left = l.Left
	m.UserConfig.Dock.Center = l.Center
	m.UserConfig.Dock.Right = l.Right
}

func (m *OS) sideList(side string) []string {
	if m.UserConfig == nil {
		return nil
	}
	return slices.Clone(m.UserConfig.Dock.DockList(side))
}

func (m *OS) writeSide(side string, list []string) {
	if m.UserConfig == nil {
		return
	}
	out := slices.Clone(list)
	switch side {
	case "left":
		m.UserConfig.Dock.Left = &out
	case "center":
		m.UserConfig.Dock.Center = &out
	default:
		m.UserConfig.Dock.Right = &out
	}
}

func (m *OS) dockEditorRows() []dockEditorRow {
	var rows []dockEditorRow
	placed := map[string]bool{}
	for _, side := range dockEditorSides {
		rows = append(rows, dockEditorRow{Kind: dockRowHeader, Side: side})
		list := m.sideList(side)
		if len(list) == 0 {
			rows = append(rows, dockEditorRow{Kind: dockRowEmpty, Side: side})
			continue
		}
		for _, name := range list {
			placed[name] = true
			rows = append(rows, dockEditorRow{
				Kind:   dockRowComponent,
				Side:   side,
				Name:   name,
				Fixed:  config.DockFixedSide(name),
				Custom: strings.HasPrefix(name, config.DockCustomPrefix),
			})
		}
	}

	var available []string
	for _, name := range config.DockBuiltinComponents() {
		if !placed[name] {
			available = append(available, name)
		}
	}
	if m.UserConfig != nil {
		for name, spec := range m.UserConfig.Dock.Custom {
			full := config.DockCustomPrefix + name
			if !placed[full] && strings.TrimSpace(spec.Command) != "" {
				available = append(available, full)
			}
		}
	}
	slices.Sort(available)

	rows = append(rows, dockEditorRow{Kind: dockRowHeader, Side: dockAvailableSide})
	if len(available) == 0 {
		rows = append(rows, dockEditorRow{Kind: dockRowEmpty, Side: dockAvailableSide})
	}
	for _, name := range available {
		rows = append(rows, dockEditorRow{
			Kind:   dockRowAvailable,
			Name:   name,
			Fixed:  config.DockFixedSide(name),
			Custom: strings.HasPrefix(name, config.DockCustomPrefix),
		})
	}
	return rows
}

const dockAvailableSide = "available"

// DockEditorCursor is the dock editor's row under the cursor.
func (m *OS) DockEditorCursor() int { return m.dockList.Cursor }

// DockEditorMove moves the selection by delta, skipping the header lines: a
// header is a label, and stopping on one costs a keystroke on the way past.
func (m *OS) DockEditorMove(delta int) {
	rows := m.dockEditorRows()
	if len(rows) == 0 {
		return
	}
	_, visible, _ := m.dockEditorLayout()
	m.dockList.Move(delta, len(rows), visible, m.listWraps(),
		func(i int) bool { return rows[i].Kind != dockRowHeader })
}

// DockEditorShift moves the selected component one place in the given direction,
// crossing into the neighbouring region when it runs off the end of its own.
//
// Crossing rather than stopping is the point: the alternative is a separate
// "which side" control, and then putting the clock on the left is two controls
// and an ordering rather than one gesture.
func (m *OS) DockEditorShift(dir int) tea.Cmd {
	rows := m.dockEditorRows()
	if m.dockList.Cursor < 0 || m.dockList.Cursor >= len(rows) {
		return nil
	}
	row := rows[m.dockList.Cursor]
	if row.Kind != dockRowComponent {
		return nil
	}

	list := m.sideList(row.Side)
	idx := slices.Index(list, row.Name)
	if idx < 0 {
		return nil
	}

	target := idx + dir
	if target >= 0 && target < len(list) {
		list[idx], list[target] = list[target], list[idx]
		m.writeSide(row.Side, list)
		m.selectDockComponent(row.Name)
		return m.commitDockLists(false)
	}

	sideIdx := slices.Index(dockEditorSides, row.Side)
	nextIdx := sideIdx + dir
	if nextIdx < 0 || nextIdx >= len(dockEditorSides) {
		return nil
	}
	nextSide := dockEditorSides[nextIdx]
	if row.Fixed != "" && row.Fixed != nextSide {
		m.ShowNotification(row.Name+" always draws on the "+row.Fixed+
			". You cannot move it.", "info", m.Settings.NotificationDuration)
		return nil
	}

	m.writeSide(row.Side, slices.Delete(list, idx, idx+1))
	next := m.sideList(nextSide)
	if dir > 0 {
		next = append([]string{row.Name}, next...)
	} else {
		next = append(next, row.Name)
	}
	m.writeSide(nextSide, next)
	m.selectDockComponent(row.Name)
	return m.commitDockLists(false)
}

// DockEditorToggle takes a placed component off the bar, or puts an available
// one back on it. Bound to Enter and to a click.
func (m *OS) DockEditorToggle() tea.Cmd {
	rows := m.dockEditorRows()
	if m.dockList.Cursor < 0 || m.dockList.Cursor >= len(rows) {
		return nil
	}
	row := rows[m.dockList.Cursor]

	switch row.Kind {
	case dockRowComponent:
		list := m.sideList(row.Side)
		if idx := slices.Index(list, row.Name); idx >= 0 {
			m.writeSide(row.Side, slices.Delete(list, idx, idx+1))
		}
	case dockRowAvailable:
		side := row.Fixed
		if side == "" {
			side = "right"
		}
		m.writeSide(side, append(m.sideList(side), row.Name))
	default:
		return nil
	}

	m.selectDockComponent(row.Name)
	return m.commitDockLists(true)
}

// DockEditorReset puts the three lists back to what the bar draws with no
// [dock] table at all.
func (m *OS) DockEditorReset() tea.Cmd {
	m.writeSide("left", config.DefaultDockLeft())
	m.writeSide("center", config.DefaultDockCenter())
	m.writeSide("right", config.DefaultDockRight())
	m.dockList.Cursor = 0
	m.dockList.Scroll = 0
	m.DockEditorMove(1)
	return m.commitDockLists(true)
}

func (m *OS) selectDockComponent(name string) {
	rows := m.dockEditorRows()
	for i, row := range rows {
		if row.Name == name && row.Kind != dockRowHeader {
			_, visible, _ := m.dockEditorLayout()
			m.dockList.Select(i, len(rows), visible)
			return
		}
	}
}

func (m *OS) commitDockLists(rebuild bool) tea.Cmd {
	if rebuild {
		cmd := m.InitDockComponents()
		m.MarkAllDirty()
		return tea.Batch(cmd, m.persistSettings())
	}
	m.rebuildDockPlan()
	m.MarkAllDirty()
	return m.persistSettings()
}
