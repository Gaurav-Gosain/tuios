package app

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// # The files section as a tree
//
// appearance.sidebar.files_style = "tree" draws the section the way neo-tree
// does: one root, the folder the list style would show, and every folder opens
// in place under its own row. The list style and the tree share the root's
// listing, its read, its watch and every file action; what the tree adds is
// the set of open folders and a listing for each one on screen.
//
// # What it costs
//
// Nothing is read until a folder is opened, and nothing is read on an idle
// client. An open folder costs its listing (capped at fileViewMaxEntries names,
// like the root) and one inotify watch on the client's single extra inotify
// instance (dirwatch.Set). A closed folder drops its listing and its watch.
// The sync that decides what to read runs once per message, and answers from
// a fingerprint comparison unless the root, the session or the tree changed.
//
// # What is remembered
//
// Which folders are open, per session, for as long as the client runs. Opening
// a folder whose parent is closed costs nothing until the parent opens again.
// The open set is not saved: it is where a person was looking, not a setting.

// fileTreeMaxOpen bounds the open folders the tree reads and watches at once.
// Past it a folder will not open, and a remembered one stays drawn closed.
const fileTreeMaxOpen = 64

// fileTreeOpenDirGlyph is the nerd font mark of an open folder, drawn in place
// of the icon table's closed folder on a folder the tree has open.
const fileTreeOpenDirGlyph = "\ue5fe"

// The expander marks neo-tree draws by default, used where the file icons
// are: a nerd font chevron right for a closed folder and down for an open one.
// Without the icons the glyph set's fold marks draw instead.
const (
	fileTreeChevronShut = "\uf460"
	fileTreeChevronOpen = "\uf47c"
)

// fileTreeDir is one open folder's listing.
type fileTreeDir struct {
	Entries []fileEntry
	Capped  bool
	Err     string
	Loading bool
	// Req stamps the read in flight. A reply that does not carry it is for a
	// read a newer one replaced, or for a folder that closed and opened again.
	Req uint64
	// Again says the folder changed while its first read was running, so it
	// is read once more when that read lands, as the root is.
	Again bool
}

// fileTreeState is the tree's runtime state. It is shared by both rail edges,
// like the root listing it hangs from.
type fileTreeState struct {
	// open is the open folders, per session, keyed by fileTreeKey.
	open map[string]map[string]struct{}
	// dirs is the listing of each open folder the tree draws now, by absolute
	// path. It never holds a folder that is not on screen.
	dirs map[string]*fileTreeDir
	// host is the machine dirs were read from.
	host string
	// req is the last read stamp handed out.
	req uint64
	// gen moves whenever what the tree draws changes. The rail's cache folds it.
	gen uint64
	// guides keeps each indent guide drawn, by its shape. There are a few
	// dozen shapes in any tree a rail can show.
	guides map[string]string
	// synced is what the last sync saw, so a sync on an idle client is one
	// comparison.
	synced fileTreeSync
}

// fileTreeSync is the fingerprint syncFileTree compares.
type fileTreeSync struct {
	on                  bool
	session, root, host string
	rootGen, treeGen    uint64
}

// fileTreeListMsg is one open folder's finished read.
type fileTreeListMsg struct {
	Dir     string
	Req     uint64
	Quiet   bool
	Entries []fileEntry
	Capped  bool
	Err     string
}

// filesTreeOn reports whether the files section draws as a tree.
func (m *OS) filesTreeOn() bool {
	return m.Settings.SidebarFilesStyle == config.SidebarFilesTree
}

// fileTreeKey names an open folder across machines: the same path on two
// machines is two folders.
func fileTreeKey(host, dir string) string { return host + "\x00" + dir }

// fileTreeOpenSet is the open set of the attached session.
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

// fileTreeVisible is the open folders the tree draws now, in drawn order: each
// is open, and so is every folder between it and the root. A folder that is
// open but whose parent is closed costs nothing.
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

// syncFileTree makes the tree's listings match the open folders on screen:
// it reads a folder that has just come into view, drops the listing of one
// that left it, and points the watch at what remains. It is called once per
// message from FilesSyncCmd, and answers nil after one comparison when nothing
// it depends on moved.
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
		// Nothing read on the last machine is about this one.
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

// fileTreeForgetGone closes the open folders that are gone from a listing on
// screen, so a folder deleted and later made again under the same name comes
// back closed, as a new folder. Only a listing read cleanly says a name is
// gone; one being read or one that failed says nothing.
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

// fileTreeRead asks for one open folder's listing. A quiet read is a re-read
// because the folder changed on disk: the names stay up and nothing is drawn
// again unless the answer differs.
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
	// A folder below the root is one the person opened by hand, so it is read
	// as one they named, the same way a list walk into it is.
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
		// Read because it changed: the change may have been the folder itself
		// being deleted and made again, which ends its watch.
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
	// The new names can hold folders the person opened before: they come into
	// view now, and are read now.
	if again == nil {
		return m.syncFileTree()
	}
	return tea.Batch(again, m.syncFileTree())
}

// refreshChangedTree re-reads the open folders the watch said changed. A
// folder in its first read is read again when that read lands. root says the
// root changed too.
//
// An open folder whose read failed has no watch, so it cannot say it came
// back. Its parent can: when a folder changes, the open folders under it that
// failed are read again with it.
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

// refreshTreeAll re-reads every open folder quietly, after a file action.
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

// fileTreeOpenDir opens a folder of the tree.
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

// fileTreeCloseDir closes a folder of the tree. The folders open under it stay
// remembered, and show again when it opens.
func (m *OS) fileTreeCloseDir(dir string) tea.Cmd {
	delete(m.fileTreeOpenSet(false), fileTreeKey(m.filesView.Host, dir))
	m.filesTree.gen++
	return m.syncFileTree()
}

// fileTreeToggleDir opens a closed folder and closes an open one.
func (m *OS) fileTreeToggleDir(dir string) tea.Cmd {
	if m.filesTree.dirs[dir] != nil {
		return m.fileTreeCloseDir(dir)
	}
	return m.fileTreeOpenDir(dir)
}

// fileTreeEntry resolves a tree row's key, the path from the root with "/"
// between names, to the folder that holds it and its entry in that folder's
// listing. A key whose folder is not listed, or whose name has left the
// listing, names nothing.
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

// fileTreeParentKey is the key of a row's folder, or "" for a row of the root.
func fileTreeParentKey(key string) string {
	if i := strings.LastIndexByte(key, '/'); i >= 0 {
		return key[:i]
	}
	return ""
}

// fileViewEnterRow is what taking a listing row does, by click or by enter.
// The list style addresses the row by its index into the listing on screen;
// the tree addresses it by its key, because a row below the root is not in
// the root's listing.
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

// fileTreeTake is the tree's answer to taking a folder row: it opens or
// closes in place.
//
// folder_click = "cd" still types the cd and opens nothing, because that is
// all the person asked a folder row to do. "both" opens it and does not cd:
// the tree follows the pane, so a cd would put the tree's root on the folder
// just opened, and the open would be undone by the same click.
func (m *OS) fileTreeTake(full string) tea.Cmd {
	if m.Settings.SidebarFolderClick == config.SidebarFolderClickCd {
		m.sendCdToOrigin(full)
		return nil
	}
	return m.fileTreeToggleDir(full)
}

// fileTreeUnderRoot reports whether full is a folder below the listed root,
// which is the only kind the tree can open in place.
func (m *OS) fileTreeUnderRoot(full string) bool {
	root := m.filesView.Dir
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(root, full)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// fileTreeCursorRow is the cursor's row when it is a name of the tree.
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

// fileTreeCursorExpand is l on a row of the tree: a closed folder opens, an
// open one puts the cursor on its first name. It reports whether the row was
// the tree's, so the key does not also step to the next section.
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

// fileTreeCursorCollapse is h on a row of the tree: an open folder closes, any
// other row puts the cursor on its folder's row. On a name of the root it does
// nothing, and reports true so the key does not leave the section either.
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

// fileTreeRows appends the tree under dir to rows, depth first, in the order
// the listing gives: folders first, then names.
//
// cont says, for each folder between the root's names and these rows, whether
// that folder has names after it. It is what draws a guide down the column of
// a folder whose rows continue below the rows open under it. more says a note
// row follows these names, so the last of them is not drawn as the last.
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

// fileTreeNote is the row an open folder adds after its names, if any: that it
// is being read, that it could not be, that it is empty, or that it was cut.
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

// fileTreeGuide is the indent in front of a row of the tree: a guide down the
// column of each folder above whose rows go on, and a corner on the last name
// of the row's own folder. Two cells per level, so each guide sits under its
// folder's open mark.
//
// The strings are kept by shape, so the thousands of rows of one folder share
// one guide string rather than each building its own.
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

// buildTreeGuide draws the guide fileTreeGuide keeps.
func (m *OS) buildTreeGuide(cont []bool, depth int, last bool) string {
	// neo-tree's two marks: a bar down the column of a folder whose names go
	// on below, and a corner on its last name. No tee and no dash, so the
	// guides stay a quiet line and do not reach for the row's mark.
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
