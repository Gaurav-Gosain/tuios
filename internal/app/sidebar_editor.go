package app

import (
	"slices"
	"strconv"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

type railRowKind int

const (
	railRowHeader railRowKind = iota
	railRowPlaced
	railRowEmpty
	railRowAvailable
)

type railEditorRow struct {
	Kind   railRowKind
	Name   string
	Index  int
	Share  int
	Spacer bool
	Last   bool
}

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
	m.sectionList.Scroll = 0
	m.SectionEditorOriginal = m.sectionLayoutRaw()

	m.sectionList.Cursor = 0
	for i, row := range m.sectionEditorRows() {
		if row.Kind != railRowHeader {
			m.sectionList.Cursor = i
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

func (m *OS) sectionLayout() string {
	if raw := m.sectionLayoutRaw(); raw != "" {
		return raw
	}
	return m.Settings.SidebarSections
}

func (m *OS) sectionLayoutRaw() string {
	if m.UserConfig == nil {
		return ""
	}
	return m.UserConfig.Appearance.Sidebar.Sections
}

func (m *OS) sectionEntries() []config.SidebarSectionShare {
	return config.ParseSidebarSections(m.sectionLayout())
}

func (m *OS) sectionEditorRows() []railEditorRow {
	entries := m.sectionEntries()
	rows := make([]railEditorRow, 0, len(entries)+len(config.SidebarSectionNames)+3)

	rows = append(rows, railEditorRow{Kind: railRowHeader, Name: railListOn, Index: -1})
	if len(entries) == 0 {
		rows = append(rows, railEditorRow{Kind: railRowEmpty, Name: railListOn, Index: -1})
	}
	hidden := func(name string) bool { return !m.agentsOn() && name == "agents" }
	placed := map[string]bool{}
	last := len(entries) - 1
	for last >= 0 && hidden(entries[last].Name) {
		last--
	}
	for i, e := range entries {
		if !e.IsSpacer() {
			placed[e.Name] = true
		}
		if hidden(e.Name) {
			continue
		}
		rows = append(rows, railEditorRow{
			Kind: railRowPlaced, Name: e.Name, Index: i, Share: e.Share,
			Spacer: e.IsSpacer(), Last: i == last,
		})
	}

	rows = append(rows, railEditorRow{Kind: railRowHeader, Name: railListOff, Index: -1})
	for _, name := range config.SidebarSectionNames {
		if placed[name] || hidden(name) {
			continue
		}
		rows = append(rows, railEditorRow{Kind: railRowAvailable, Name: name, Index: -1})
	}
	rows = append(rows, railEditorRow{
		Kind: railRowAvailable, Name: config.SidebarSectionSpacer, Index: -1, Spacer: true,
	})
	return rows
}

// SectionEditorCursor is the section editor's row under the cursor.
func (m *OS) SectionEditorCursor() int { return m.sectionList.Cursor }

// SectionEditorMove moves the selection by delta, skipping the headings: a
// heading is a label, and stopping on one costs a keystroke on the way past.
func (m *OS) SectionEditorMove(delta int) {
	rows := m.sectionEditorRows()
	if len(rows) == 0 {
		return
	}
	_, visible, _ := m.sectionEditorLayout()
	m.sectionList.Move(delta, len(rows), visible, m.listWraps(),
		func(i int) bool { return rows[i].Kind != railRowHeader })
}

// SectionEditorShift moves the selected entry one place up or down the rail.
//
// It stops at the ends rather than wrapping. The dock's editor crosses into the
// next region there because it has three lists to cross between; the rail has
// one, and past its end there is nothing to move into.
func (m *OS) SectionEditorShift(dir int) tea.Cmd {
	rows := m.sectionEditorRows()
	if m.sectionList.Cursor < 0 || m.sectionList.Cursor >= len(rows) {
		return nil
	}
	row := rows[m.sectionList.Cursor]
	if row.Kind != railRowPlaced {
		return nil
	}
	entries := m.sectionEntries()
	target := row.Index + dir
	if row.Index < 0 || row.Index >= len(entries) || target < 0 || target >= len(entries) {
		return nil
	}
	entries[row.Index], entries[target] = entries[target], entries[row.Index]
	m.sectionList.Cursor += dir
	return m.commitSectionLayout(entries)
}

// SectionEditorToggle takes a section off the rail, or puts one back on it, and
// on the spacer row adds one more spacer. Bound to Enter and to a click.
func (m *OS) SectionEditorToggle() tea.Cmd {
	rows := m.sectionEditorRows()
	if m.sectionList.Cursor < 0 || m.sectionList.Cursor >= len(rows) {
		return nil
	}
	row := rows[m.sectionList.Cursor]
	entries := m.sectionEntries()

	switch row.Kind {
	case railRowPlaced:
		if row.Index < 0 || row.Index >= len(entries) {
			return nil
		}
		if !row.Spacer && sectionCount(entries, m.agentsOn()) <= 1 {
			m.ShowNotification("The rail keeps one section. Add another first.",
				"info", m.Settings.NotificationDuration)
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
	if m.sectionList.Cursor < 0 || m.sectionList.Cursor >= len(rows) {
		return nil
	}
	row := rows[m.sectionList.Cursor]
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

const sectionShareStep = 5

// SectionEditorReset puts the layout back to the one the rail ships with.
func (m *OS) SectionEditorReset() tea.Cmd {
	cmd := m.commitSectionLayout(config.ParseSidebarSections(config.SidebarDefaultSections))
	m.sectionList.Reset()
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

func sectionCount(entries []config.SidebarSectionShare, agentsOn bool) int {
	n := 0
	for _, e := range entries {
		if !e.IsSpacer() && (agentsOn || e.Name != "agents") {
			n++
		}
	}
	return n
}

func (m *OS) selectSectionRow(name string, added bool) {
	want := railRowAvailable
	if added {
		want = railRowPlaced
	}
	rows := m.sectionEditorRows()
	for i, row := range rows {
		if row.Name == name && row.Kind == want {
			_, visible, _ := m.sectionEditorLayout()
			m.sectionList.Select(i, len(rows), visible)
			return
		}
	}
	_, visible, _ := m.sectionEditorLayout()
	m.sectionList.Fit(len(rows), visible)
}

func (m *OS) commitSectionLayout(entries []config.SidebarSectionShare) tea.Cmd {
	return m.commitSectionString(config.SidebarSectionsString(entries))
}

func (m *OS) commitSectionString(layout string) tea.Cmd {
	m.setOption("appearance.sidebar.sections", layout)
	if !sidebarLayoutHas(sidebarSectionFiles, &m.Settings) {
		m.filesView.Show = 0
	}
	m.MarkAllDirty()
	return m.persistSettings()
}

func sectionShareLabel(share int) string {
	if share <= 0 {
		return "auto"
	}
	return strconv.Itoa(share) + "%"
}
