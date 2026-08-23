package app

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The dock is three ordered lists, which is the one thing on the settings page
// that no row can express. A cycler has no order, a toggle has no place to put
// what it turns off, and a text field holding a TOML array is the config file
// with extra steps and no validation.
//
// So it is an editor: the three regions and their components as one list, with
// what is not placed underneath it. Moving a component is one keystroke, and it
// crosses into the next region when it runs off the end of its own, which is
// what makes "put the clock on the left" a thing you do rather than a thing you
// look up.

// dockEditorSides is the regions in draw order, which is also the order they
// are listed in.
var dockEditorSides = []string{"left", "center", "right"}

// dockRowKind is what one line of the editor is.
type dockRowKind int

const (
	dockRowHeader    dockRowKind = iota // a region's name
	dockRowComponent                    // a placed component
	dockRowEmpty                        // a region with nothing in it
	dockRowAvailable                    // a component that is defined and not placed
)

// dockEditorRow is one line: enough to draw it and to act on it.
type dockEditorRow struct {
	Kind dockRowKind
	Side string // the region, for a header or a placed component
	Name string // the component, for a component or available row
	// Fixed is the side this component always draws on whatever list names it,
	// empty for one that can be moved.
	Fixed string
	// Custom marks a cell from a [dock.custom.NAME] table rather than a
	// built-in, which is worth saying because its behaviour is the user's own.
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
	m.DockEditorScroll = 0
	m.DockEditorOriginal = m.dockLists()

	// Land on the first component rather than on the first header, so the first
	// keystroke does something.
	m.DockEditorSelected = 0
	for i, row := range m.dockEditorRows() {
		if row.Kind == dockRowComponent {
			m.DockEditorSelected = i
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

// dockLists is a snapshot of the three lists, for cancel to restore.
//
// A nil list is kept nil rather than resolved to the default it stands for. The
// difference is what a config file records: nil is "[dock] says nothing", and a
// cancel that put the defaults back as three explicit lists would leave the
// file naming a layout the user never chose, pinned to today's defaults.
type dockLists struct{ Left, Center, Right *[]string }

// dockLists reads the current lists in a form that can be compared and restored.
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

// equal reports whether two snapshots name the same layout, an unset list
// counting as different from the same list written out.
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

// setDockLists writes a snapshot back onto the config.
func (m *OS) setDockLists(l dockLists) {
	if m.UserConfig == nil {
		return
	}
	m.UserConfig.Dock.Left = l.Left
	m.UserConfig.Dock.Center = l.Center
	m.UserConfig.Dock.Right = l.Right
}

// sideList is one region's components.
func (m *OS) sideList(side string) []string {
	if m.UserConfig == nil {
		return nil
	}
	return slices.Clone(m.UserConfig.Dock.DockList(side))
}

// writeSide replaces one region's list.
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

// dockEditorRows is the flattened list the editor draws and navigates.
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

	// What is defined and not on the bar. Without this, removing a component
	// would be one-way from inside the editor and the only way back would be
	// the config file, which is the trip the editor exists to save.
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

// dockAvailableSide is the pseudo-region holding what is not on the bar.
const dockAvailableSide = "available"

// DockEditorMove moves the selection by delta, skipping the header lines: a
// header is a label, and stopping on one costs a keystroke on the way past.
func (m *OS) DockEditorMove(delta int) {
	rows := m.dockEditorRows()
	if len(rows) == 0 {
		return
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	next := m.DockEditorSelected
	for range max(delta, -delta) {
		candidate := next
		for {
			candidate += step
			if candidate < 0 || candidate >= len(rows) {
				candidate = next
				break
			}
			if rows[candidate].Kind != dockRowHeader {
				break
			}
		}
		next = candidate
	}
	m.DockEditorSelected = clampInt(next, 0, len(rows)-1)
	m.scrollDockEditor(len(rows))
}

// scrollDockEditor keeps the selection inside the visible window.
func (m *OS) scrollDockEditor(total int) {
	_, visible, _ := m.dockEditorLayout()
	m.DockEditorScroll = scrollWindow(m.DockEditorScroll, m.DockEditorSelected, total, visible)
}

// DockEditorShift moves the selected component one place in the given direction,
// crossing into the neighbouring region when it runs off the end of its own.
//
// Crossing rather than stopping is the point: the alternative is a separate
// "which side" control, and then putting the clock on the left is two controls
// and an ordering rather than one gesture.
func (m *OS) DockEditorShift(dir int) tea.Cmd {
	rows := m.dockEditorRows()
	if m.DockEditorSelected < 0 || m.DockEditorSelected >= len(rows) {
		return nil
	}
	row := rows[m.DockEditorSelected]
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

	// Off the end of this region, so into the next one.
	sideIdx := slices.Index(dockEditorSides, row.Side)
	nextIdx := sideIdx + dir
	if nextIdx < 0 || nextIdx >= len(dockEditorSides) {
		return nil
	}
	nextSide := dockEditorSides[nextIdx]
	if row.Fixed != "" && row.Fixed != nextSide {
		// A pinned component draws on its own side whatever list names it, so
		// moving it would change the file and nothing on screen. Saying so
		// beats an edit that appears to do nothing.
		m.ShowNotification(row.Name+" always draws on the "+row.Fixed+
			", so it cannot be moved to the "+nextSide, "info", config.NotificationDuration)
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
	if m.DockEditorSelected < 0 || m.DockEditorSelected >= len(rows) {
		return nil
	}
	row := rows[m.DockEditorSelected]

	switch row.Kind {
	case dockRowComponent:
		list := m.sideList(row.Side)
		if idx := slices.Index(list, row.Name); idx >= 0 {
			m.writeSide(row.Side, slices.Delete(list, idx, idx+1))
		}
	case dockRowAvailable:
		// Onto the side it is pinned to where it has one, so a component that
		// can only draw on the right does not land in a list that will not
		// draw it.
		side := row.Fixed
		if side == "" {
			side = "right"
		}
		m.writeSide(side, append(m.sideList(side), row.Name))
	default:
		return nil
	}

	m.selectDockComponent(row.Name)
	// The set of placed components changed, so the engine is rebuilt: which
	// components are scheduled comes from the plan.
	return m.commitDockLists(true)
}

// DockEditorReset puts the three lists back to what the bar draws with no
// [dock] table at all.
func (m *OS) DockEditorReset() tea.Cmd {
	m.writeSide("left", config.DefaultDockLeft())
	m.writeSide("center", config.DefaultDockCenter())
	m.writeSide("right", config.DefaultDockRight())
	m.DockEditorSelected = 0
	m.DockEditorScroll = 0
	m.DockEditorMove(1)
	return m.commitDockLists(true)
}

// selectDockComponent puts the selection back on a component after the lists
// moved under it, so the row a person is dragging stays under the cursor.
func (m *OS) selectDockComponent(name string) {
	for i, row := range m.dockEditorRows() {
		if row.Name == name && row.Kind != dockRowHeader {
			m.DockEditorSelected = i
			m.scrollDockEditor(len(m.dockEditorRows()))
			return
		}
	}
}

// commitDockLists applies an edit and hands back the save.
//
// rebuild says whether the set of placed components changed. A reorder does not
// change which components exist, so it costs a re-plan and a repaint; going
// through the engine for it would stop and restart every custom component's
// process on each keystroke, and a push component would lose its stream every
// time the list moved.
func (m *OS) commitDockLists(rebuild bool) tea.Cmd {
	if rebuild {
		cmd := m.InitDockComponents()
		m.MarkAllDirty()
		return tea.Batch(cmd, m.persistSettings())
	}
	m.dockPlan = buildDockPlan(m.UserConfig)
	m.MarkAllDirty()
	return m.persistSettings()
}
