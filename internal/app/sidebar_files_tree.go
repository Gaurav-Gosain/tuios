package app

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

const fileTreeMaxOpen = 64

const fileTreeOpenDirGlyph = "\ue5fe"

const (
	fileTreeChevronShut = "\uf460"
	fileTreeChevronOpen = "\uf47c"
)

type fileTreeDir struct {
	Entries []fileEntry
	Capped  bool
	Err     string
	Loading bool
	Req     uint64
	Again   bool
}

type fileTreeState struct {
	open   map[string]map[string]struct{}
	dirs   map[string]*fileTreeDir
	host   string
	req    uint64
	gen    uint64
	guides map[string]string
	synced fileTreeSync
}

type fileTreeSync struct {
	on                  bool
	session, root, host string
	rootGen, treeGen    uint64
}

type fileTreeListMsg struct {
	Dir     string
	Req     uint64
	Quiet   bool
	Entries []fileEntry
	Capped  bool
	Err     string
}

func (m *OS) filesTreeOn() bool {
	return m.Settings.SidebarFilesStyle == config.SidebarFilesTree
}

func fileTreeKey(host, dir string) string { return host + "\x00" + dir }

func (m *OS) fileTreeOpenSet(create bool) map[string]struct{} {
	sid := m.sidebarCurrentSessionID()
	set := m.filesTree.open[sid]
	if set == nil && create {
		if m.filesTree.open == nil {
			m.filesTree.open = map[string]map[string]struct{}{}
		}
		set = map[string]struct{}{}
		m.filesTree.open[sid] = set
	}
	return set
}

func (m *OS) fileTreeVisible() []string {
	v := m.filesView
	if !m.filesTreeOn() || v.Dir == "" || v.Err != "" || v.Elsewhere != "" {
		return nil
	}
	set := m.fileTreeOpenSet(false)
	if len(set) == 0 {
		return nil
	}
	var out []string
	var walk func(dir string, entries []fileEntry)
	walk = func(dir string, entries []fileEntry) {
		for _, e := range entries {
			if !e.Dir || len(out) >= fileTreeMaxOpen {
				continue
			}
			full := filepath.Join(dir, e.Name)
			if _, ok := set[fileTreeKey(v.Host, full)]; !ok {
				continue
			}
			out = append(out, full)
			if d := m.filesTree.dirs[full]; d != nil {
				walk(full, d.Entries)
			}
		}
	}
	walk(v.Dir, v.Entries)
	return out
}

func (m *OS) syncFileTree() tea.Cmd {
	v := m.filesView
	t := &m.filesTree
	fp := fileTreeSync{
		on:      m.filesTreeOn() && v.Dir != "",
		session: m.sidebarCurrentSessionID(),
		root:    v.Dir, host: v.Host,
		rootGen: v.Gen, treeGen: t.gen,
	}
	if fp == t.synced {
		return nil
	}
	changed := false
	if t.host != v.Host {
		if len(t.dirs) > 0 {
			t.dirs = nil
			changed = true
		}
		t.host = v.Host
	}
	m.fileTreeForgetGone()
	visible := m.fileTreeVisible()
	want := make(map[string]bool, len(visible))
	for _, d := range visible {
		want[d] = true
	}
	for d := range t.dirs {
		if !want[d] {
			delete(t.dirs, d)
			changed = true
		}
	}
	var cmds []tea.Cmd
	for _, d := range visible {
		if t.dirs[d] == nil {
			cmds = append(cmds, m.fileTreeRead(d, false))
			changed = true
		}
	}
	if len(t.dirs) == 0 {
		t.dirs = nil
	}
	if changed {
		t.gen++
	}
	fp.treeGen = t.gen
	t.synced = fp
	m.syncTreeWatch(visible)
	return tea.Batch(cmds...)
}

func (m *OS) fileTreeForgetGone() {
	set := m.fileTreeOpenSet(false)
	v := m.filesView
	if len(set) == 0 || v.Dir == "" || v.Loading || v.Err != "" {
		return
	}
	prefix := fileTreeKey(v.Host, "")
	for key := range set {
		dir, ok := strings.CutPrefix(key, prefix)
		if !ok {
			continue
		}
		parent, name := filepath.Dir(dir), filepath.Base(dir)
		var entries []fileEntry
		switch d := m.filesTree.dirs[parent]; {
		case parent == v.Dir:
			entries = v.Entries
		case d != nil && !d.Loading && d.Err == "":
			entries = d.Entries
		default:
			continue
		}
		if !slices.ContainsFunc(entries, func(e fileEntry) bool { return e.Dir && e.Name == name }) {
			delete(set, key)
		}
	}
}

func (m *OS) fileTreeRead(dir string, quiet bool) tea.Cmd {
	t := &m.filesTree
	if t.dirs == nil {
		t.dirs = map[string]*fileTreeDir{}
	}
	d := t.dirs[dir]
	if d == nil {
		d = &fileTreeDir{}
		t.dirs[dir] = d
	}
	t.req++
	req := t.req
	d.Req = req
	if !quiet {
		d.Loading = true
	}
	client, host, origin := m.DaemonClient, m.AttachedHost, m.filesView.Origin
	return func() tea.Msg {
		l := readListing(client, host, origin, dir, true, 0, false)
		return fileTreeListMsg{Dir: dir, Req: req, Quiet: quiet, Entries: l.Entries, Capped: l.Capped, Err: l.Err}
	}
}

// HandleFileTreeList applies an open folder's read, or drops it.
func (m *OS) HandleFileTreeList(msg fileTreeListMsg) tea.Cmd {
	d := m.filesTree.dirs[msg.Dir]
	if d == nil || d.Req != msg.Req {
		return nil
	}
	if msg.Quiet && msg.Err == "" {
		m.rewatchTree(msg.Dir)
	}
	var again tea.Cmd
	if d.Again {
		d.Again = false
		again = m.fileTreeRead(msg.Dir, true)
	}
	if msg.Quiet && !d.Loading && d.Err == msg.Err && d.Capped == msg.Capped && slices.Equal(d.Entries, msg.Entries) {
		return again
	}
	d.Loading = false
	d.Entries, d.Capped, d.Err = msg.Entries, msg.Capped, msg.Err
	m.filesTree.gen++
	if again == nil {
		return m.syncFileTree()
	}
	return tea.Batch(again, m.syncFileTree())
}

func (m *OS) refreshChangedTree(dirs []string, root bool) tea.Cmd {
	if root && m.filesView.Dir != "" {
		dirs = append(dirs, m.filesView.Dir)
	}
	var cmds []tea.Cmd
	read := func(dir string) {
		d := m.filesTree.dirs[dir]
		switch {
		case d == nil:
		case d.Loading:
			d.Again = true
		default:
			cmds = append(cmds, m.fileTreeRead(dir, true))
		}
	}
	for _, dir := range dirs {
		read(dir)
		for child, d := range m.filesTree.dirs {
			if d.Err != "" && !d.Loading && filepath.Dir(child) == dir {
				read(child)
			}
		}
	}
	return tea.Batch(cmds...)
}

func (m *OS) refreshTreeAll() tea.Cmd {
	if !m.filesTreeOn() {
		return nil
	}
	dirs := make([]string, 0, len(m.filesTree.dirs))
	for dir := range m.filesTree.dirs {
		dirs = append(dirs, dir)
	}
	return m.refreshChangedTree(dirs, false)
}

func (m *OS) fileTreeOpenDir(dir string) tea.Cmd {
	if m.filesTree.dirs[dir] != nil {
		return nil
	}
	if len(m.filesTree.dirs) >= fileTreeMaxOpen {
		m.ShowNotification("You can open "+strconv.Itoa(fileTreeMaxOpen)+" folders at most. Close a folder first.",
			"info", m.Settings.NotificationDuration)
		return nil
	}
	m.fileTreeOpenSet(true)[fileTreeKey(m.filesView.Host, dir)] = struct{}{}
	m.filesTree.gen++
	return m.syncFileTree()
}

func (m *OS) fileTreeCloseDir(dir string) tea.Cmd {
	delete(m.fileTreeOpenSet(false), fileTreeKey(m.filesView.Host, dir))
	m.filesTree.gen++
	return m.syncFileTree()
}

func (m *OS) fileTreeToggleDir(dir string) tea.Cmd {
	if m.filesTree.dirs[dir] != nil {
		return m.fileTreeCloseDir(dir)
	}
	return m.fileTreeOpenDir(dir)
}

func (m *OS) fileTreeEntry(key string) (parent string, e fileEntry, ok bool) {
	root := m.filesView.Dir
	if root == "" || key == "" {
		return "", fileEntry{}, false
	}
	entries := m.filesView.Entries
	parent = root
	name := key
	if i := strings.LastIndexByte(key, '/'); i >= 0 {
		parent = filepath.Join(root, filepath.FromSlash(key[:i]))
		name = key[i+1:]
		d := m.filesTree.dirs[parent]
		if d == nil {
			return "", fileEntry{}, false
		}
		entries = d.Entries
	}
	for _, x := range entries {
		if x.Name == name {
			return parent, x, true
		}
	}
	return "", fileEntry{}, false
}

func fileTreeParentKey(key string) string {
	if i := strings.LastIndexByte(key, '/'); i >= 0 {
		return key[:i]
	}
	return ""
}

func (m *OS) fileViewEnterRow(index int, key string) tea.Cmd {
	if !m.filesTreeOn() {
		return m.FileViewEnter(index)
	}
	parent, e, ok := m.fileTreeEntry(key)
	if !ok {
		return nil
	}
	return m.fileViewOpen(parent, e.Name, e.Dir)
}

func (m *OS) fileTreeTake(full string) tea.Cmd {
	if m.Settings.SidebarFolderClick == config.SidebarFolderClickCd {
		m.sendCdToOrigin(full)
		return nil
	}
	return m.fileTreeToggleDir(full)
}

func (m *OS) fileTreeUnderRoot(full string) bool {
	root := m.filesView.Dir
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(root, full)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (m *OS) fileTreeCursorRow() (sidebarNavRow, bool) {
	if !m.filesTreeOn() || !m.filesOn() {
		return sidebarNavRow{}, false
	}
	row, ok := m.sidebarCursorRow()
	if !ok || row.Kind != sidebarRowFileEntry {
		return sidebarNavRow{}, false
	}
	return row, true
}

func (m *OS) fileTreeCursorExpand() bool {
	row, ok := m.fileTreeCursorRow()
	if !ok {
		return false
	}
	parent, e, ok := m.fileTreeEntry(row.WindowID)
	if !ok || !e.Dir {
		return true
	}
	full := filepath.Join(parent, e.Name)
	d := m.filesTree.dirs[full]
	if d == nil {
		m.queueSidebarCmd(m.fileTreeOpenDir(full))
		return true
	}
	if len(d.Entries) > 0 {
		m.sidebarWantCursor(sidebarNavRow{
			Kind: sidebarRowFileEntry, WindowID: row.WindowID + "/" + d.Entries[0].Name, WindowIndex: -1,
		})
	}
	return true
}

func (m *OS) fileTreeCursorCollapse() bool {
	row, ok := m.fileTreeCursorRow()
	if !ok {
		return false
	}
	if parent, e, ok := m.fileTreeEntry(row.WindowID); ok && e.Dir {
		if full := filepath.Join(parent, e.Name); m.filesTree.dirs[full] != nil {
			m.queueSidebarCmd(m.fileTreeCloseDir(full))
			return true
		}
	}
	if up := fileTreeParentKey(row.WindowID); up != "" {
		idx := -1
		if !strings.Contains(up, "/") {
			for i, x := range m.filesView.Entries {
				if x.Name == up {
					idx = i
					break
				}
			}
		}
		m.sidebarWantCursor(sidebarNavRow{Kind: sidebarRowFileEntry, WindowID: up, WindowIndex: idx})
	}
	return true
}

func (m *OS) fileTreeRows(rows []fileRowSpec, dir, key string, entries []fileEntry, cont []bool, more bool) []fileRowSpec {
	depth := 0
	if key != "" {
		depth = len(cont) + 1
	}
	for i, e := range entries {
		last := i == len(entries)-1 && !more
		k := e.Name
		if key != "" {
			k = key + "/" + e.Name
		}
		var sub *fileTreeDir
		if e.Dir {
			sub = m.filesTree.dirs[filepath.Join(dir, e.Name)]
		}
		idx := -1
		if key == "" {
			idx = i
		}
		rows = append(rows, fileRowSpec{
			Kind: sidebarRowFileEntry, Index: idx, Key: k, Name: e.Name, Dir: e.Dir, Icon: e.Icon,
			Tree: true, Depth: depth, Guide: m.fileTreeGuide(cont, depth, last), Open: sub != nil,
		})
		if sub == nil {
			continue
		}
		childCont := cont
		if depth > 0 {
			childCont = append(slices.Clone(cont), !last)
		}
		note := fileTreeNote(sub)
		rows = m.fileTreeRows(rows, filepath.Join(dir, e.Name), k, sub.Entries, childCont, note.Name != "")
		if note.Name != "" {
			note.Tree, note.Depth = true, len(childCont)+1
			note.Guide = m.fileTreeGuide(childCont, note.Depth, true)
			rows = append(rows, note)
		}
	}
	return rows
}

func fileTreeNote(d *fileTreeDir) fileRowSpec {
	switch {
	case d.Err != "":
		return fileRowSpec{Note: true, Warn: true, Name: d.Err}
	case d.Loading:
		return fileRowSpec{Note: true, Name: "loading"}
	case d.Capped:
		return fileRowSpec{Note: true, Name: "first " + strconv.Itoa(fileViewMaxEntries) + " shown"}
	case len(d.Entries) == 0:
		return fileRowSpec{Note: true, Name: "empty"}
	}
	return fileRowSpec{}
}

func (m *OS) fileTreeGuide(cont []bool, depth int, last bool) string {
	if depth == 0 {
		return ""
	}
	var shape [64]byte
	n := 0
	for _, c := range cont[:min(len(cont), depth-1, len(shape)-1)] {
		shape[n] = '0'
		if c {
			shape[n] = '1'
		}
		n++
	}
	shape[n] = 'm'
	if last {
		shape[n] = 'l'
	}
	n++
	if m.Settings.UseASCIIOnly {
		shape[n] = 'a'
		n++
	}
	if g, ok := m.filesTree.guides[string(shape[:n])]; ok {
		return g
	}
	g := m.buildTreeGuide(cont, depth, last)
	if m.filesTree.guides == nil {
		m.filesTree.guides = map[string]string{}
	}
	m.filesTree.guides[string(shape[:n])] = g
	return g
}

func (m *OS) buildTreeGuide(cont []bool, depth int, last bool) string {
	bar, corner := "│", "└"
	if m.Settings.UseASCIIOnly {
		bar, corner = "|", "`"
	}
	var b strings.Builder
	for _, c := range cont[:min(len(cont), depth-1)] {
		if c {
			b.WriteString(bar)
			b.WriteByte(' ')
		} else {
			b.WriteString("  ")
		}
	}
	if last {
		b.WriteString(corner)
	} else {
		b.WriteString(bar)
	}
	b.WriteByte(' ')
	return b.String()
}
