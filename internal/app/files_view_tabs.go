package app

import (
	"path"

	tea "charm.land/bubbletea/v2"
)

// Tabs and sides. A tab holds one side, or two side by side; \ turns the
// second on and off, tab moves the keys between them, < and > move the
// divider, and Z shows the side with the keys alone. [ and ] go through the
// tabs, + opens one on the folder of the side with the keys, and ctrl+w
// closes one. Between 80 and 99 columns a tab of two sides draws one side
// at a time, and tab flips between them.

// filesTabCur is the tab on screen.
func (m *OS) filesTabCur() *filesTab {
	e := &m.explorer
	if len(e.tabs) == 0 {
		t := &filesTab{ratio: filesRatioDefault}
		t.sides[0] = m.newFilesSide("")
		e.tabs = []*filesTab{t}
		e.tab = 0
	}
	e.tab = max(min(e.tab, len(e.tabs)-1), 0)
	return e.tabs[e.tab]
}

// filesCur is the side with the keys.
func (m *OS) filesCur() *filesSide { return m.filesTabCur().cur() }

// newFilesSide is an empty side on host, with the sort and the switches a
// person chose last.
func (m *OS) newFilesSide(host string) *filesSide {
	e := &m.explorer
	e.ids++
	return &filesSide{host: host, id: e.ids, sort: e.sort, reverse: e.reverse, hidden: e.hidden}
}

// filesSideByID is the side a read was for, or nil when it is gone.
func (m *OS) filesSideByID(id uint64) *filesSide {
	for _, t := range m.explorer.tabs {
		for _, s := range t.sides {
			if s != nil && s.id == id {
				return s
			}
		}
	}
	return nil
}

// filesCopySide is a new side on the machine and folder of s.
func (m *OS) filesCopySide(s *filesSide) (*filesSide, string) {
	n := m.newFilesSide(s.host)
	n.home = s.home
	dir := s.Dir
	if dir == "" {
		dir = s.want
	}
	return n, dir
}

// FilesTwoSides turns the second side on or off. A second side opens on the
// folder of the side with the keys, and takes the keys.
func (m *OS) FilesTwoSides() tea.Cmd {
	t := m.filesTabCur()
	m.explorer.gen++
	if t.two {
		t.two, t.zoom = false, false
		return nil
	}
	t.two = true
	if t.sides[1] == nil {
		n, dir := m.filesCopySide(t.sides[0])
		t.sides[1] = n
		t.focus = 1
		return m.filesReadSide(n, dir, "")
	}
	t.focus = 1
	return nil
}

// FilesOtherSide gives the keys to the other side.
func (m *OS) FilesOtherSide() {
	t := m.filesTabCur()
	if !t.two || t.sides[1-t.focus] == nil {
		return
	}
	t.focus = 1 - t.focus
	t.zoom = false
	m.explorer.gen++
}

// filesFocusSide gives the keys to side i of the tab on screen.
func (m *OS) filesFocusSide(i int) {
	t := m.filesTabCur()
	if i == t.focus || t.sides[i] == nil {
		return
	}
	t.focus = i
	m.explorer.gen++
}

// FilesDivider moves the divider by delta percent, held between a fifth and
// four fifths of the width.
func (m *OS) FilesDivider(delta int) {
	t := m.filesTabCur()
	if !t.shown() {
		return
	}
	t.ratio = max(min(t.ratio+delta, 80), 20)
	m.explorer.gen++
}

// filesDividerAt puts the divider at screen column x.
func (m *OS) filesDividerAt(x int) {
	t := m.filesTabCur()
	w := m.GetRenderWidth()
	if !t.shown() || w <= 0 {
		return
	}
	t.ratio = max(min(x*100/w, 80), 20)
	m.explorer.gen++
}

// FilesDividerReset puts the divider half way.
func (m *OS) FilesDividerReset() {
	m.filesTabCur().ratio = filesRatioDefault
	m.explorer.gen++
}

// FilesZoom shows the side with the keys alone, or both sides again.
func (m *OS) FilesZoom() {
	t := m.filesTabCur()
	if !t.two {
		return
	}
	t.zoom = !t.zoom
	m.explorer.gen++
}

// FilesNextTab goes d tabs on, round the ends.
func (m *OS) FilesNextTab(d int) {
	e := &m.explorer
	if n := len(e.tabs); n > 1 {
		e.tab = ((e.tab+d)%n + n) % n
		e.gen++
	}
}

// FilesGoTab goes to tab i, counted from 0. A tab that is not there does
// nothing.
func (m *OS) FilesGoTab(i int) {
	e := &m.explorer
	if i >= 0 && i < len(e.tabs) && i != e.tab {
		e.tab = i
		e.gen++
	}
}

// FilesNewTab opens a tab on the folder of the side with the keys, after
// the tab on screen.
func (m *OS) FilesNewTab() tea.Cmd {
	e := &m.explorer
	n, dir := m.filesCopySide(m.filesCur())
	t := &filesTab{ratio: filesRatioDefault}
	t.sides[0] = n
	at := e.tab + 1
	e.tabs = append(e.tabs[:at], append([]*filesTab{t}, e.tabs[at:]...)...)
	e.tab = at
	e.gen++
	return m.filesReadSide(n, dir, "")
}

// FilesCloseTab closes the tab on screen. The last tab closes the view.
func (m *OS) FilesCloseTab() {
	e := &m.explorer
	if len(e.tabs) <= 1 {
		m.CloseFilesView()
		return
	}
	e.tabs = append(e.tabs[:e.tab], e.tabs[e.tab+1:]...)
	e.tab = min(e.tab, len(e.tabs)-1)
	e.gen++
}

// filesTabName is the name of tab t on the top bar: the machine and the
// folder of one side, or the two sides' machines with a two-way arrow. Two
// sides on one machine are named by their folders.
func (m *OS) filesTabName(t *filesTab, arrow string) string {
	a := t.sides[0]
	if !t.two || t.sides[1] == nil {
		name, _ := m.filesMachineOf(a)
		return name + " " + a.short(filesSideDir(a))
	}
	b := t.sides[1]
	na, _ := m.filesMachineOf(a)
	nb, _ := m.filesMachineOf(b)
	if na != nb {
		return na + " " + arrow + " " + nb
	}
	return na + " " + path.Base(filesSideDir(a)) + " " + arrow + " " + path.Base(filesSideDir(b))
}

// filesSideDir is the folder of s, or the one asked for before a read lands.
func filesSideDir(s *filesSide) string {
	if s.Dir != "" {
		return s.Dir
	}
	return s.want
}
