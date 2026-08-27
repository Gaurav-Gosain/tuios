package app

import (
	"slices"
	"strconv"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The rail's layout is an ordered list with a number on each entry, which is
// the one shape a settings row cannot hold. It was a text field until now:
// "sessions:25,terminals,files:25,agents:34", typed by hand, with a colon that
// means one thing and a comma that means another, and no way to see what you
// had done until you closed the panel.
//
// So it is an editor, built the way the dock's is, because a person who has
// moved a clock onto the left of the dock should not have to learn a second set
// of habits to move the files list up the rail. Same keys, same two lists, same
// applied-as-you-go saving.
//
// # Membership lives here and nowhere else
//
// There used to be two ways to take a section off the rail: leave it out of the
// layout, or set the boolean named after it. The layout won, and the spacer is
// why. A layout may carry two spacers, in two different places, and a boolean
// per section has nowhere to put the second one and no way to say which gap it
// means. So the list owns order, share and membership together, the booleans
// fold into it on load, and this editor is where a section is turned off.

// railRowKind is what one line of the editor is.
type railRowKind int

const (
	railRowHeader    railRowKind = iota // one of the two lists' names
	railRowPlaced                       // a section or a spacer on the rail
	railRowEmpty                        // a list with nothing in it
	railRowAvailable                    // a section that is not on the rail
)

// railEditorRow is one line: enough to draw it and to act on it.
type railEditorRow struct {
	Kind railRowKind
	// Name is the section, or "spacer".
	Name string
	// Index is the entry's place in the layout for a placed row, and -1 for
	// every other kind.
	Index int
	// Share is the percent the entry may claim, or zero for the flexible one
	// that takes what the others leave.
	Share int
	// Spacer marks the empty block, which is the one entry that may appear more
	// than once and so is always offered.
	Spacer bool
	// Last marks the bottom entry of the rail. A spacer there is a gap at the
	// end, which is a different sentence from a gap in the middle.
	Last bool
}

// railEditorLists are the two headings, in the words the panel uses.
const (
	railListOn  = "On the rail"
	railListOff = "Not on the rail"
)

// OpenSectionEditor shows the rail layout editor, remembering the layout so
// undo can put it back.
func (m *OS) OpenSectionEditor() {
	if m.UserConfig == nil {
		m.UserConfig = config.DefaultConfig()
		m.ConfigReadOnly = true
	}
	m.ShowSectionEditor = true
	m.SectionEditorScroll = 0
	m.SectionEditorOriginal = m.sectionLayoutRaw()

	// Land on the first entry rather than on the heading, so the first keystroke
	// does something.
	m.SectionEditorSelected = 0
	for i, row := range m.sectionEditorRows() {
		if row.Kind != railRowHeader {
			m.SectionEditorSelected = i
			break
		}
	}

	if m.ShowSettings {
		so := m.overlayOffset("settings")
		m.setOverlayOffset("sectioneditor", so[0]+8, so[1]+2)
	}
}

// CloseSectionEditor hides the editor, keeping the layout as it is.
func (m *OS) CloseSectionEditor() {
	m.ShowSectionEditor = false
}

// sectionLayout is the live layout string, resolved: the config's own value, or
// the shipped one where the config names none.
func (m *OS) sectionLayout() string {
	if raw := m.sectionLayoutRaw(); raw != "" {
		return raw
	}
	return config.SidebarSections
}

// sectionLayoutRaw is the layout as the config file holds it, empty included.
//
// Undo works on this rather than on the resolved layout, so an editor opened on
// a config that named no layout leaves it naming none. The difference is what a
// config file records: empty is "[appearance.sidebar] says nothing", and an
// undo that wrote the four sections back out would leave the file naming a
// layout the user never chose, pinned to today's defaults.
func (m *OS) sectionLayoutRaw() string {
	if m.UserConfig == nil {
		return ""
	}
	return m.UserConfig.Appearance.Sidebar.Sections
}

// sectionEntries is the live layout, parsed.
func (m *OS) sectionEntries() []config.SidebarSectionShare {
	return config.ParseSidebarSections(m.sectionLayout())
}

// sectionEditorRows is the flattened list the editor draws and navigates: what
// is on the rail, then what is not.
//
// The spacer sits at the bottom of the second list and never leaves it. It is
// not a section that is off the rail, it is a thing you may add as many of as
// you like, so "add it and it disappears from here" would be wrong twice: once
// for the first spacer and again for the second.
func (m *OS) sectionEditorRows() []railEditorRow {
	entries := m.sectionEntries()
	rows := make([]railEditorRow, 0, len(entries)+len(config.SidebarSectionNames)+3)

	rows = append(rows, railEditorRow{Kind: railRowHeader, Name: railListOn, Index: -1})
	if len(entries) == 0 {
		rows = append(rows, railEditorRow{Kind: railRowEmpty, Name: railListOn, Index: -1})
	}
	placed := map[string]bool{}
	last := len(entries) - 1
	for i, e := range entries {
		if !e.IsSpacer() {
			placed[e.Name] = true
		}
		rows = append(rows, railEditorRow{
			Kind: railRowPlaced, Name: e.Name, Index: i, Share: e.Share,
			Spacer: e.IsSpacer(), Last: i == last,
		})
	}

	rows = append(rows, railEditorRow{Kind: railRowHeader, Name: railListOff, Index: -1})
	for _, name := range config.SidebarSectionNames {
		if placed[name] {
			continue
		}
		rows = append(rows, railEditorRow{Kind: railRowAvailable, Name: name, Index: -1})
	}
	rows = append(rows, railEditorRow{
		Kind: railRowAvailable, Name: config.SidebarSectionSpacer, Index: -1, Spacer: true,
	})
	return rows
}

// SectionEditorMove moves the selection by delta, skipping the headings: a
// heading is a label, and stopping on one costs a keystroke on the way past.
func (m *OS) SectionEditorMove(delta int) {
	rows := m.sectionEditorRows()
	if len(rows) == 0 {
		return
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	next := m.SectionEditorSelected
	for range max(delta, -delta) {
		candidate := next
		for {
			candidate += step
			if candidate < 0 || candidate >= len(rows) {
				candidate = next
				break
			}
			if rows[candidate].Kind != railRowHeader {
				break
			}
		}
		next = candidate
	}
	m.SectionEditorSelected = clampInt(next, 0, len(rows)-1)
	m.scrollSectionEditor(len(rows))
}

// scrollSectionEditor keeps the selection inside the visible window.
func (m *OS) scrollSectionEditor(total int) {
	_, visible, _ := m.sectionEditorLayout()
	m.SectionEditorScroll = scrollWindow(m.SectionEditorScroll, m.SectionEditorSelected, total, visible)
}

// SectionEditorShift moves the selected entry one place up or down the rail.
//
// It stops at the ends rather than wrapping. The dock's editor crosses into the
// next region there because it has three lists to cross between; the rail has
// one, and past its end there is nothing to move into.
func (m *OS) SectionEditorShift(dir int) tea.Cmd {
	rows := m.sectionEditorRows()
	if m.SectionEditorSelected < 0 || m.SectionEditorSelected >= len(rows) {
		return nil
	}
	row := rows[m.SectionEditorSelected]
	if row.Kind != railRowPlaced {
		return nil
	}
	entries := m.sectionEntries()
	target := row.Index + dir
	if row.Index < 0 || row.Index >= len(entries) || target < 0 || target >= len(entries) {
		return nil
	}
	entries[row.Index], entries[target] = entries[target], entries[row.Index]
	m.SectionEditorSelected += dir
	return m.commitSectionLayout(entries)
}

// SectionEditorToggle takes a section off the rail, or puts one back on it, and
// on the spacer row adds one more spacer. Bound to Enter and to a click.
func (m *OS) SectionEditorToggle() tea.Cmd {
	rows := m.sectionEditorRows()
	if m.SectionEditorSelected < 0 || m.SectionEditorSelected >= len(rows) {
		return nil
	}
	row := rows[m.SectionEditorSelected]
	entries := m.sectionEntries()

	switch row.Kind {
	case railRowPlaced:
		if row.Index < 0 || row.Index >= len(entries) {
			return nil
		}
		if !row.Spacer && sectionCount(entries) <= 1 {
			// The rail has to draw something. An empty layout falls back to the
			// shipped one on the next parse, so the edit would appear to undo
			// itself, which is worse than being told no.
			m.ShowNotification("The rail keeps one section. Add another first.",
				"info", config.NotificationDuration)
			return nil
		}
		entries = slices.Delete(entries, row.Index, row.Index+1)
	case railRowAvailable:
		entries = append(entries, config.SidebarSectionShare{Name: row.Name})
	default:
		return nil
	}

	cmd := m.commitSectionLayout(entries)
	m.selectSectionRow(row.Name, row.Kind == railRowAvailable)
	return cmd
}

// SectionEditorShare walks the selected entry's share by five points. Zero is
// "auto": the entry takes whatever the others leave.
//
// Five rather than one because the share is a ceiling on a rail thirty lines
// tall, where one point is a third of a line and twenty keystrokes is a quarter
// of the rail.
func (m *OS) SectionEditorShare(delta int) tea.Cmd {
	rows := m.sectionEditorRows()
	if m.SectionEditorSelected < 0 || m.SectionEditorSelected >= len(rows) {
		return nil
	}
	row := rows[m.SectionEditorSelected]
	if row.Kind != railRowPlaced {
		return nil
	}
	entries := m.sectionEntries()
	if row.Index < 0 || row.Index >= len(entries) {
		return nil
	}
	share := clampInt(entries[row.Index].Share+delta*sectionShareStep, 0, 100)
	if share == entries[row.Index].Share {
		return nil
	}
	entries[row.Index].Share = share
	return m.commitSectionLayout(entries)
}

// sectionShareStep is how far one keystroke moves a share.
const sectionShareStep = 5

// SectionEditorReset puts the layout back to the one the rail ships with.
func (m *OS) SectionEditorReset() tea.Cmd {
	cmd := m.commitSectionLayout(config.ParseSidebarSections(config.SidebarDefaultSections))
	m.SectionEditorSelected, m.SectionEditorScroll = 0, 0
	m.SectionEditorMove(1)
	return cmd
}

// SectionEditorRevert puts the layout back as it was when the editor opened.
//
// An undo rather than what Esc does, for the reason the dock's editor gives:
// every edit here is applied and saved as it is made, so the rail has been
// showing the new layout for as long as the person has been looking at it.
func (m *OS) SectionEditorRevert() tea.Cmd {
	if m.sectionLayoutRaw() == m.SectionEditorOriginal {
		return nil
	}
	return m.commitSectionString(m.SectionEditorOriginal)
}

// sectionCount is how many real sections a layout has. Spacers do not count:
// a rail of nothing but empty blocks draws nothing at all.
func sectionCount(entries []config.SidebarSectionShare) int {
	n := 0
	for _, e := range entries {
		if !e.IsSpacer() {
			n++
		}
	}
	return n
}

// selectSectionRow puts the selection back on a row after the list moved under
// it, so the entry a person is working on stays under the cursor.
//
// added says the row was just put on the rail, so the selection follows it into
// the first list rather than staying on the second one's now-missing line.
func (m *OS) selectSectionRow(name string, added bool) {
	want := railRowAvailable
	if added {
		want = railRowPlaced
	}
	rows := m.sectionEditorRows()
	for i, row := range rows {
		if row.Name == name && row.Kind == want {
			m.SectionEditorSelected = i
			m.scrollSectionEditor(len(rows))
			return
		}
	}
	m.SectionEditorSelected = clampInt(m.SectionEditorSelected, 0, max(len(rows)-1, 0))
	m.scrollSectionEditor(len(rows))
}

// commitSectionLayout writes the layout, applies it and hands back the save.
//
// The rail's render cache is keyed on the layout string, so applying it is what
// makes the next frame the new one. MarkAllDirty is here for the panes: a rail
// that changed width or a section that appeared moves the ground under them.
func (m *OS) commitSectionLayout(entries []config.SidebarSectionShare) tea.Cmd {
	return m.commitSectionString(config.SidebarSectionsString(entries))
}

// commitSectionString writes one layout string, applies it and hands back the
// save. An empty string is a layout the config does not name, which applies as
// the shipped one.
func (m *OS) commitSectionString(layout string) tea.Cmd {
	m.setOption("appearance.sidebar.sections", layout)
	m.MarkAllDirty()
	return m.persistSettings()
}

// sectionShareLabel is a share as the editor says it.
func sectionShareLabel(share int) string {
	if share <= 0 {
		return "auto"
	}
	return strconv.Itoa(share) + "%"
}
