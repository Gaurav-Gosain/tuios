package app

import (
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// The Files view's machine picker. A click on a side's machine row, or M,
// lists the machines the side can show: this machine, every host with the
// state of its link, and the ssh hosts of ~/.ssh/config that are not hosts
// yet. It is the window picker's list with a purpose of its own, so the
// rows, the filter and the keys are the same.

// filesWaitHost is a side that opens on a machine once its link is up: a
// host the picker added, or one that was offline when it was picked.
type filesWaitHost struct {
	side uint64
	host string
}

// FilesPickMachine opens the machine picker for the side with the keys.
func (m *OS) FilesPickMachine() {
	s := m.filesCur()
	if s == nil {
		return
	}
	m.explorer.machineFor = s.id
	m.HostPickerPurpose = HostPickerFilesSide
	m.HostPickerItems = m.filesMachineItems()
	m.hostList.Reset()
	m.hostList.Cursor = FirstPickableHostRow(m.HostPickerItems)
	m.ShowHostPicker = true
	m.raiseOverlay("hostpicker")
}

// filesMachineItems is this machine, then every host with its link's state,
// then the ssh hosts that are not hosts yet, under their own heading.
func (m *OS) filesMachineItems() []HostPickerItem {
	items := []HostPickerItem{{Label: thisMachineLabel(), Up: true}}
	known := map[string]bool{}
	for _, h := range m.FederationHosts {
		if h.Name == federation.LocalHostName {
			continue
		}
		known[h.Name] = true
		up := h.Status == string(federation.StatusUp)
		detail := hostStatusLabel(h.Status)
		switch {
		case up && h.RTTMillis > 0:
			detail = "linked · " + strconv.FormatInt(h.RTTMillis, 10) + " ms"
		case up:
			detail = "linked"
		case h.Reason != "":
			detail += ": " + h.Reason
		}
		items = append(items, HostPickerItem{Name: h.Name, Label: h.Name, Detail: detail, Up: up})
	}
	for _, name := range m.configuredHostNames() {
		if !known[name] {
			known[name] = true
			items = append(items, HostPickerItem{Name: name, Label: name, Detail: "connecting"})
		}
	}
	var add []HostPickerItem
	for _, alias := range m.hostAddrCandidates() {
		if !known[alias] {
			add = append(add, HostPickerItem{Name: alias, Label: alias, Detail: "add", Add: true, Up: true})
		}
	}
	if len(add) > 0 {
		items = append(items, HostPickerItem{Label: "Add with ssh", Section: true})
		items = append(items, add...)
	}
	return items
}

// ChooseHostForFilesSide shows the picked machine on the side the picker
// was opened for. A host to add runs the add sheet, and a host that is not
// up is dialled again; either way the side opens there once the link is up.
func (m *OS) ChooseHostForFilesSide(item HostPickerItem) tea.Cmd {
	if item.Section || item.Pending {
		return nil
	}
	m.CloseHostPicker()
	e := &m.explorer
	s := m.filesSideByID(e.machineFor)
	if !e.open || s == nil {
		return nil
	}
	switch {
	case item.Add:
		e.waitHost = filesWaitHost{side: s.id, host: item.Name}
		return m.OpenMachineSheet(item.Name)
	case item.Name != "" && !m.hostIsUp(item.Name):
		e.waitHost = filesWaitHost{side: s.id, host: item.Name}
		m.ShowNotification(printableTitle(item.Name)+" is not linked ("+item.Detail+"). tuios tries again now, and the side opens there once the link is up.", "info", m.Settings.NotificationDuration*2)
		return retryHostCmd(item.Name)
	}
	return m.filesShowMachine(s, item.Name)
}

// filesShowMachine shows host on side s, in the folder the side shows when
// that folder is there too, else in the home folder.
func (m *OS) filesShowMachine(s *filesSide, host string) tea.Cmd {
	if host == m.AttachedHost {
		host = ""
	}
	dir := "~"
	if s.Dir != "" {
		dir = s.short(s.Dir)
	}
	if host == s.host && dir == s.short(s.Dir) {
		return nil
	}
	s.host, s.home = host, ""
	s.marks, s.filter = nil, ""
	s.homeIfMissing = dir != "~"
	m.explorer.gen++
	return m.filesReadSide(s, dir, "")
}

// filesMachineUp opens a side that waits for a machine once the snapshot
// says the link is up.
func (m *OS) filesMachineUp() tea.Cmd {
	e := &m.explorer
	w := e.waitHost
	if !e.open || w.host == "" || !m.hostIsUp(w.host) {
		return nil
	}
	e.waitHost = filesWaitHost{}
	s := m.filesSideByID(w.side)
	if s == nil {
		return nil
	}
	return m.filesShowMachine(s, w.host)
}

// filesMachineRowHit reports whether y is the machine row of side s.
func filesMachineRowHit(s *filesSide, y int) bool {
	return s.crumbsY > 0 && y == s.crumbsY-1
}
