package main

import (
	"bytes"
	"path"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"
	"github.com/Gaurav-Gosain/mygo/ui/editor"

	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/guios/git"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The Files tab is the tree of the worktree of the pane the user is in,
// else of its directory, as kerod lists it (fs.list), a folder at a time
// as the user unfolds it: what git ignores shows faded, and .git not at
// all. A click on a file opens it in the viewer, as text the user edits
// (edits.go), which follows the file as it changes.

// filesState is the Files tab's.
type filesState struct {
	// dirs are the folders listed, open the folders unfolded, by host and
	// path (filesKey); places the trees' places, by host and root.
	dirs   map[string]*dirList
	open   map[string]bool
	places map[string]*ui.ListState
	// gen counts the times the tab came to show, each of which lists its
	// folders anew; shown tells it shows this frame, and showed that it
	// did the frame before.
	gen           int
	shown, showed bool
	// edit is the name the user types in the tree (files_ops.go).
	edit *nameEdit
	// cursor is the row the keys are on, by path: j and k move it, space
	// shows its file in the viewer, and the viewer follows it while open
	// (the TUI's quick look keys).
	cursor string
}

// dirList is a folder as kerod listed it.
type dirList struct {
	entries []proto.FSEntry
	err     string
	busy    bool
	stamp   string // the worktree's status it was listed at (gitStamp)
	gen     int
	fetched time.Time
}

// fileItem is a row of the tree: a file or a folder, depth folders in, or
// why a folder shows nothing, err, or the field the user types a name in,
// edit.
type fileItem struct {
	proto.FSEntry
	path      string // on the host
	rel       string // in the root, with slashes
	parent    string // the folder it is in, on the host
	parentRel string
	depth     int
	open      bool
	err       string
	edit      bool
}

// fileView is a file the viewer shows, as its text.
type fileView struct {
	pane  string // a pane of the host it is on
	path  string // on the host
	rel   string // in the Files' root, which its header shows
	st    *editor.State
	hash  string // of the file as its text shows it (git.Hash)
	edits edited
	err   string
	// gone, large and binary say why it shows no text: there is no file
	// there any more, or one too large, or one that isn't text.
	gone, large, binary bool
	busy                bool
	stamp               string
	fetched             time.Time
	// look is what was read to show it as what it is (look.go); nil for
	// a file of another machine, which shows as its text.
	look *fileLook
}

// maxView bounds the files the viewer shows as text.
const maxView = editMax

// filesKey keys a path of a host.
func filesKey(h *host, p string) string { return h.name + "\x00" + p }

// filesRoot is the folder the Files tab shows, and the pane it is of: the
// worktree of the one the user is in, else its directory.
func (a *App) filesRoot(s *proto.Session) (*proto.Pane, string) {
	p := withGit(s, s.PaneByID(a.focusOf(s)))
	switch {
	case p == nil:
		return nil, ""
	case p.Git != nil:
		return p, p.Git.Root
	case p.ProcessDir != "":
		return p, p.ProcessDir
	}
	return p, p.Dir
}

// filesSide is the Files tab: the root's tree, a folder listed as it
// unfolds, and again as the worktree changes.
func (a *App) filesSide(c *ui.Context, pal *palette, s *proto.Session) {
	fs := &a.side.files
	if !fs.showed {
		fs.gen++
	}
	fs.shown, fs.showed = true, true
	p, root := a.filesRoot(s)
	if root == "" {
		a.sideEmpty(c, pal, folderIcon, locales.FilesNoFolder(), locales.FilesNoFolderDetail())
		return
	}
	h := a.hostOf(p.ID)
	if fs.dirs == nil || len(fs.dirs) > 2000 {
		fs.dirs = map[string]*dirList{}
	}
	if fs.open == nil {
		fs.open = map[string]bool{}
	}
	stamp := gitStamp(p.Git)
	working := sessionWorking(s)
	if working {
		c.After(5 * time.Second)
	}
	asking := 0
	for _, l := range fs.dirs {
		if l.busy {
			asking++
		}
	}
	// The folders shown, listed again as the worktree's status changes,
	// as the tab shows anew, and every few seconds while an agent works,
	// a few at a time.
	list := func(dir string) *dirList {
		l := fs.dirs[filesKey(h, dir)]
		stale := l == nil || !l.busy && (l.stamp != stamp || l.gen != fs.gen || working && time.Since(l.fetched) > 5*time.Second)
		if stale && asking < 4 && h.cl != nil {
			asking++
			l = a.fetchDir(h, dir, p.Git != nil, stamp)
		}
		return l
	}
	var items []fileItem
	var walk func(dir, rel string, depth int)
	walk = func(dir, rel string, depth int) {
		l := list(dir)
		if l == nil || l.entries == nil && l.err == "" {
			return
		}
		if l.err != "" && l.entries == nil {
			items = append(items, fileItem{path: dir, rel: rel + "/!", depth: depth, err: l.err})
			return
		}
		// A new entry's name, at the top of its folder.
		if e := fs.edit; e != nil && e.op != editRename && e.host == h.name && e.dir == dir {
			items = append(items, fileItem{FSEntry: proto.FSEntry{Dir: e.op == editNewFolder}, rel: rel + "/+", parent: dir, parentRel: rel, depth: depth, edit: true})
		}
		for _, e := range l.entries {
			if e.Dir && e.Name == ".git" {
				continue
			}
			it := fileItem{FSEntry: e, path: h.joinPath(dir, e.Name), rel: path.Join(rel, e.Name), parent: dir, parentRel: rel, depth: depth}
			it.open = e.Dir && fs.open[filesKey(h, it.path)]
			it.edit = fs.edit != nil && fs.edit.op == editRename && fs.edit.host == h.name && fs.edit.path == it.path
			items = append(items, it)
			if it.open {
				walk(it.path, it.rel, depth+1)
			}
		}
	}
	walk(root, "", 0)

	ui.Row(c.Key("root")).Height(28).Shrink(0).Padding(0, 10, 6, 16).Gap(0).AlignItems(ui.Center).Children(func() {
		ui.Text(c, h.shortDir(root)).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine().Ellipsis("…").MinWidth(0).Grow(1).Tooltip(root)
		iconButton(c, "new-file", newFileIcon, locales.FilesNewFile(), pal, func() { a.startCreate(s, p.ID, root, "", false) })
		iconButton(c, "new-folder", newFolderIcon, locales.FilesNewFolder(), pal, func() { a.startCreate(s, p.ID, root, "", true) })
		iconButton(c, "fold-folders", collapseIcon, locales.FilesCollapseFolders(), pal, func() {
			prefix := filesKey(h, root)
			for k := range fs.open {
				if strings.HasPrefix(k, prefix) {
					delete(fs.open, k)
				}
			}
		})
	})
	if l := fs.dirs[filesKey(h, root)]; l == nil || l.entries == nil {
		switch {
		case l != nil && l.err != "" && !l.busy:
			a.sideMessage(c, pal, locales.FilesCantReadFolder(), l.err)
		case h.cl != nil:
			ui.Column(c).Grow(1).Center().Children(func() { ui.Spinner(c) })
		}
		return
	}

	if fs.places == nil {
		fs.places = map[string]*ui.ListState{}
	}
	ls := fs.places[filesKey(h, root)]
	if ls == nil {
		ls = &ui.ListState{}
		fs.places[filesKey(h, root)] = ls
	}
	ls.Key = func(i int) any { return treeKey{items[i].rel, items[i].Dir} }
	var shown string
	if v := a.viewOf(s); v != nil && v.kind == sideFiles && v.file != nil {
		shown = v.file.path
	}
	sid, pid := s.ID, p.ID
	// open shows a file in the viewer, in the place of the one it shows,
	// saved first; focus gives the viewer the keyboard.
	open := func(it fileItem, focus bool) {
		s := a.session(sid)
		if s == nil {
			return
		}
		v := a.side.views[sid]
		if v != nil && v.file != nil && v.file.path == it.path {
			a.openView(s, sideFiles, nil) // the one it shows
			if focus {
				a.focusViewer()
			}
			return
		}
		var ts []editTarget
		if v != nil && v.file != nil {
			if t := a.fileTarget(v.file); t.dirty() {
				ts = append(ts, t)
			}
		}
		a.askToSave(ts, func() {
			if s := a.session(sid); s != nil {
				a.openView(s, sideFiles, &fileView{pane: pid, path: it.path, rel: it.rel})
				if focus {
					a.focusViewer()
				}
			}
		})
	}
	tree := ui.Column(c.Key("tree-keys")).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Focusable().Bind(a.paneRef("files-tree"))
	focused := tree.FocusWithin()
	cur := -1
	for i := range items {
		if items[i].path == fs.cursor && !items[i].edit && items[i].err == "" {
			cur = i
		}
	}
	tree.Children(func() {
		ui.List(c.Key("files"), ls, len(items), func(i int) {
			it := items[i]
			var row ui.Element
			switch {
			case it.edit:
				a.nameRow(c, pal, it)
				return
			case it.err != "":
				ui.Text(c, it.err).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine().Ellipsis("…").Padding(4, 8, 4, 6+12*float32(it.depth)+28).Tooltip(it.err)
				return
			case it.Dir:
				key := filesKey(h, it.path)
				row = a.treeFolder(c, pal, it.Name, it.rel, it.depth, it.open, it.Ignored, func() {
					fs.cursor = it.path
					if it.open {
						delete(fs.open, key)
					} else {
						fs.open[key] = true
					}
				})
			default:
				row = a.treeEntry(c, pal, it, it.path == shown, func() {
					fs.cursor = it.path
					open(it, true)
				})
			}
			if focused && i == cur {
				row.Border(1, pal.accent.Alpha(0.75))
			}
			row.ContextMenu(func(m *ui.Menu) { a.entryMenu(m, s, pid, it) })
		}).Grow(1).MinHeight(0).Padding(0, 10, 16, 10)
	})
	if tree.Pressed() {
		a.paneRef("files-tree").Focus()
	}
	a.treeKeys(tree, ls, s, items, cur, shown, open)
}

// treeKeys are the Files tree's keys, the TUI's rail and quick look keys:
// j and k (and the arrows) move the cursor, l and h open and close a
// folder, space shows the file in the viewer or closes it, Enter opens it
// with the keyboard in it, Tab goes into the viewer, and while the viewer
// shows a file it follows the cursor, and J, K, ctrl+d, ctrl+u, f, t and v
// act on it, as in the TUI.
func (a *App) treeKeys(tree ui.Element, ls *ui.ListState, s *proto.Session, items []fileItem, cur int, shown string, open func(fileItem, bool)) {
	fs := &a.side.files
	h := a.hostOf(s.ID)
	move := func(d int) {
		n := len(items)
		if n == 0 {
			return
		}
		i := cur
		for k := 0; k < n; k++ {
			if i < 0 {
				i = 0
				if d < 0 {
					i = n - 1
				}
			} else {
				i = min(max(i+d, 0), n-1)
			}
			if !items[i].edit && items[i].err == "" {
				break
			}
		}
		it := items[i]
		fs.cursor = it.path
		ls.ScrollIntoView(i)
		// The viewer follows, unless its text has edits to save.
		if v := a.viewOf(s); v != nil && v.kind == sideFiles && v.file != nil && !it.Dir && it.path != shown {
			if t := a.fileTarget(v.file); !t.dirty() {
				open(it, false)
			}
		}
	}
	if tree.Shortcut(0, ui.KeyJ) || tree.Shortcut(0, ui.KeyDown) {
		move(1)
	}
	if tree.Shortcut(0, ui.KeyK) || tree.Shortcut(0, ui.KeyUp) {
		move(-1)
	}
	if tree.Shortcut(0, ui.KeyHome) {
		fs.cursor = ""
		move(1)
	}
	if tree.Shortcut(ui.Shift, ui.KeyG) || tree.Shortcut(0, ui.KeyEnd) {
		fs.cursor = ""
		move(-1)
	}
	if cur < 0 {
		return
	}
	it := items[cur]
	key := filesKey(h, it.path)
	switch {
	case tree.Shortcut(0, ui.KeyL) || tree.Shortcut(0, ui.KeyRight):
		if it.Dir {
			fs.open[key] = true
		}
	case tree.Shortcut(0, ui.KeyH) || tree.Shortcut(0, ui.KeyLeft):
		if it.Dir && it.open {
			delete(fs.open, key)
		} else if it.parent != "" {
			fs.cursor = it.parent
		}
	case tree.Shortcut(0, ui.KeySpace):
		v := a.viewOf(s)
		switch {
		case it.Dir:
			if it.open {
				delete(fs.open, key)
			} else {
				fs.open[key] = true
			}
		case v != nil && v.kind == sideFiles && v.file != nil && v.file.path == it.path:
			a.closeView(s)
		default:
			// Quick look: over the panes, so no terminal rewraps, unless
			// the viewer already shows beside them.
			quick := v == nil || v.kind != sideFiles
			open(it, false)
			if v := a.viewOf(s); v != nil && quick {
				v.full = true
			}
		}
	case tree.Shortcut(0, ui.KeyEnter):
		if it.Dir {
			if it.open {
				delete(fs.open, key)
			} else {
				fs.open[key] = true
			}
		} else {
			open(it, true)
		}
	}
	v := a.viewOf(s)
	if v == nil || v.kind != sideFiles || v.file == nil {
		return
	}
	l := v.file.look
	if tree.Shortcut(0, ui.KeyTab) {
		a.focusViewer()
	}
	if tree.Shortcut(0, ui.KeyEscape) {
		a.closeView(s)
	}
	if l == nil || !l.detected {
		return
	}
	if tree.Shortcut(ui.Shift, ui.KeyJ) || tree.Shortcut(ui.Shift, ui.KeyDown) {
		a.lookSeek(l, 1)
	}
	if tree.Shortcut(ui.Shift, ui.KeyK) || tree.Shortcut(ui.Shift, ui.KeyUp) {
		a.lookSeek(l, -1)
	}
	if tree.Shortcut(ui.Ctrl, ui.KeyD) || tree.Shortcut(0, ui.KeyPageDown) {
		a.lookHalf(l, 1)
	}
	if tree.Shortcut(ui.Ctrl, ui.KeyU) || tree.Shortcut(0, ui.KeyPageUp) {
		a.lookHalf(l, -1)
	}
	if tree.Shortcut(0, ui.KeyF) {
		v.full = !v.full
	}
	if tree.Shortcut(0, ui.KeyV) {
		a.setView(l, l.view+1)
	}
	if tree.Shortcut(0, ui.KeyT) && l.film != nil {
		l.film.strip = !l.film.strip
		if l.film.strip {
			a.readStrip(l)
		}
	}
}

// treeEntry is a file of the Files tab's tree, which a click opens in the
// viewer; faded, it is one git ignores.
func (a *App) treeEntry(c *ui.Context, pal *palette, it fileItem, current bool, open func()) ui.Element {
	row := treeRow(c, pal, it.depth, current).Tooltip(it.rel)
	if it.Ignored {
		row.Opacity(0.55)
	}
	if row.Clicked() {
		open()
	}
	row.Children(func() {
		ui.Box(c).Size(9, 9).Shrink(0)
		fileIconImage(c, pal, it.Name, false, false)
		ui.Text(c, it.Name).FontSize(fsBody).TextColor(pal.text).SingleLine().Ellipsis("…").MinWidth(0)
	})
	return row
}

// fetchDir asks kerod for a folder's entries, dotfiles too, marking what
// git ignores in a worktree.
func (a *App) fetchDir(h *host, dir string, ignored bool, stamp string) *dirList {
	fs := &a.side.files
	key := filesKey(h, dir)
	l := fs.dirs[key]
	if l == nil {
		l = &dirList{}
		fs.dirs[key] = l
	}
	l.busy = true
	gen, cl := fs.gen, h.cl
	go func() {
		var res proto.FSList
		err := cl.Call("fs.list", proto.FSListParams{Dir: dir, Hidden: true, Ignored: ignored}, &res)
		a.update(func() {
			l.busy, l.fetched, l.stamp, l.gen = false, time.Now(), stamp, gen
			if err != nil {
				l.err = err.Error()
				return
			}
			l.entries, l.err = res.Entries, ""
		})
	}()
	return l
}

// fileViewer is the viewer of a file of Files: the file as what it is
// (look.go), or its text, which it reads again as the worktree changes, and
// every few seconds while an agent works, while the user has no edits of it
// to save.
func (a *App) fileViewer(c *ui.Context, pal *palette, s *proto.Session, fv *fileView, width float32) {
	h := a.hostOf(fv.pane)
	if fv.look == nil && !h.remote() {
		fv.look = a.openLook(fv.path)
	}
	l := fv.look
	if l != nil {
		// The pixels a picture is first decoded at: the viewer's width on
		// a 2x screen, at least 1024; zoom asks for more.
		l.viewPx = min(max(int(width*2), 1024), firstPx)
	}
	stamp := ""
	if p := withGit(s, s.PaneByID(fv.pane)); p != nil {
		stamp = gitStamp(p.Git)
	}
	working := sessionWorking(s)
	if working {
		c.After(3 * time.Second)
	}
	text := l == nil || l.wantsText()
	editing := fv.st != nil && (fv.st.Buffer.Modified() || fv.edits.saving)
	stale := fv.fetched.IsZero() || fv.stamp != stamp || working && time.Since(fv.fetched) > 3*time.Second
	switch {
	case text && !fv.busy && !editing && h.cl != nil && stale:
		a.loadFile(h.cl, fv, stamp)
	case !text && l.detected && stale:
		// A file shown as what it is reads again when it changed.
		fv.fetched, fv.stamp = time.Now(), stamp
		a.checkLook(fv)
	}

	dir, name := path.Split(fv.rel)
	a.viewerHeader(c, pal, s, fileIcon, name, editing, func() {
		if dir = strings.TrimSuffix(dir, "/"); dir != "" {
			ui.Text(c, dir).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine().Ellipsis("…").Shrink(2).MinWidth(0).Tooltip(fv.path)
		}
	}, func() {
		if v := a.viewOf(s); v != nil {
			full, ic := locales.LookFillWindow(), zoomIcon
			if v.full {
				full, ic = locales.LookBesidePanes(), unzoomIcon
			}
			iconButton(c, "full", ic, full, pal, func() { v.full = !v.full })
		}
		if !h.remote() && !fv.gone {
			full := fv.path
			iconButton(c, "open", openFileIcon, locales.ChangesOpenFile(), pal, func() { openPath(full) })
		}
	})
	a.lookBar(c, pal, l)
	if l != nil && !text {
		a.lookBody(c, pal, fv)
		return
	}
	switch {
	case fv.gone:
		a.sideMessage(c, pal, locales.FilesGone(), fv.rel)
	case fv.large:
		a.sideMessage(c, pal, locales.ChangesTooLarge(), "")
	case fv.binary:
		a.sideMessage(c, pal, locales.ChangesBinary(), "")
	case fv.st == nil && fv.err != "":
		a.sideMessage(c, pal, locales.FilesCantReadFile(), fv.err)
	case fv.st == nil:
		ui.Column(c).Grow(1).Center().Children(func() { ui.Spinner(c) })
	default:
		fv.st.Font = ui.Font{Family: a.termFamily(), Size: max(a.set.FontSize-1, 10)}
		fv.st.Theme = a.codeTheme(c, pal)
		fv.st.NoScrollPastEnd = true
		editor.View(c.Key("text-"+fv.path), fv.st).Grow(1).FillWidth().MinHeight(0)
	}
}

// loadFile reads a file of the viewer: its text into its editor, anew for
// its first, else as an edit, so that it stays scrolled where it was.
func (a *App) loadFile(cl *client.Client, fv *fileView, stamp string) {
	fv.busy = true
	go func() {
		var st proto.FSStat
		var data []byte
		err := cl.Call("fs.stat", proto.FSStatParams{Path: fv.path}, &st)
		if err == nil && st.Exists && !st.Dir && st.Size <= maxView {
			var r proto.FSRead
			err = cl.Call("fs.read", proto.FSReadParams{Path: fv.path, Max: maxView}, &r)
			data = r.Data
		}
		a.update(func() {
			fv.busy, fv.fetched, fv.stamp = false, time.Now(), stamp
			if err != nil {
				fv.err = err.Error()
				return
			}
			fv.err = ""
			fv.gone = !st.Exists || st.Dir
			fv.large = !fv.gone && st.Size > maxView
			fv.binary = !fv.gone && !fv.large && isBinary(data)
			if fv.gone || fv.large || fv.binary {
				return
			}
			// Not over what the user typed as it came.
			if fv.st != nil && (fv.st.Buffer.Modified() || fv.edits.saving) {
				return
			}
			a.showFile(fv, data)
			if fv.look != nil {
				fv.look.mark("text shown")
			}
		})
	}()
}

// showFile puts a file as read in its viewer, and with its first text the
// user's edit of it backed up as the app last quit, if any.
func (a *App) showFile(fv *fileView, data []byte) {
	first := fv.st == nil
	fv.hash = git.Hash(data)
	showFileText(fv, string(data))
	if first {
		a.restoreEdit(sideFiles, a.hostOf(fv.pane), fv.path, fv.st.Buffer, &fv.hash)
	}
}

// showFileText puts a file's text in its viewer's editor, as saved.
func showFileText(fv *fileView, text string) {
	if fv.st == nil {
		buf := editor.NewBuffer(text)
		buf.SetLanguage(editor.LanguageFor(path.Base(fv.rel)))
		fv.st = &editor.State{Buffer: buf}
		return
	}
	replaceText(fv.st.Buffer, text)
	fv.st.Buffer.MarkSaved()
}

// isBinary tells a file that isn't text, as git does: a NUL in its first
// 8000 bytes.
func isBinary(data []byte) bool {
	return bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0
}
