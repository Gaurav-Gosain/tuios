package main

import (
	"path"
	"runtime"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/mygo"
	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The Files tab makes files and folders, renames and deletes them, as
// VS Code's explorer does: New File and New Folder, in its header for the
// root and in a row's menu for its folder, put a field for the name in
// the tree, where the entry will be, which Return makes, as does leaving
// it, and Escape leaves; a name with slashes makes the folders in it.
// Rename puts the field in the entry's place. Delete asks first, and moves
// a file of this machine to the trash, but deletes a remote host's for
// good, as kerod has no trash there to move it to.

// nameEdit is the name the user types in the tree: of a file or a folder
// to make in dir, or for an entry to rename.
type nameEdit struct {
	op      string // editNewFile, editNewFolder or editRename
	session string
	pane    string // a pane of the host
	host    string // its name
	dir     string // the folder it is in, on the host
	rel     string // that folder's, in the root
	path    string // the entry renamed, and its rel
	oldRel  string
	dirEnt  bool // the entry renamed is a folder
	name    string
	frames  int
}

const (
	editNewFile   = "file"
	editNewFolder = "folder"
	editRename    = "rename"
)

// startCreate puts the field for a new file's name, or a new folder's, at
// the top of a folder, which it unfolds.
func (a *App) startCreate(s *proto.Session, pane, dir, rel string, folder bool) {
	h := a.hostOf(pane)
	op := editNewFile
	if folder {
		op = editNewFolder
	}
	a.side.files.edit = &nameEdit{op: op, session: s.ID, pane: pane, host: h.name, dir: dir, rel: rel}
	a.side.files.open[filesKey(h, dir)] = true
}

// startRenameEntry puts the field for an entry's new name in its place.
func (a *App) startRenameEntry(s *proto.Session, pane string, it fileItem) {
	a.side.files.edit = &nameEdit{op: editRename, session: s.ID, pane: pane, host: a.hostOf(pane).name, dir: it.parent, rel: it.parentRel,
		path: it.path, oldRel: it.rel, dirEnt: it.Dir, name: it.Name}
}

// nameRow is the field the user types a name in, in the tree.
func (a *App) nameRow(c *ui.Context, pal *palette, it fileItem) {
	fs := &a.side.files
	e := fs.edit
	row := treeRow(c, pal, it.depth, true)
	row.Children(func() {
		ui.Box(c).Size(9, 9).Shrink(0)
		// The icon of the name as typed.
		fileIconImage(c, pal, strings.TrimSpace(e.name), e.op == editNewFolder || e.op == editRename && e.dirEnt, false)
		in := ui.TextInputBase(c.Key("name"), &e.name).Grow(1).MinWidth(0).FontSize(fsBody).AutoFocus().Label(it.Name)
		switch {
		case in.Shortcut(0, ui.KeyEscape):
			fs.edit = nil
		case in.Submitted():
			a.commitName()
		case !in.Focused() && !in.Pressed() && in.Valid():
			// Leaving it makes the entry, once it took the focus.
			if e.frames < 3 {
				e.frames++
				c.Invalidate()
			} else {
				a.commitName()
			}
		}
	})
}

// commitName makes the file or folder named, or renames the entry.
func (a *App) commitName() {
	fs := &a.side.files
	e := fs.edit
	fs.edit = nil
	if e == nil {
		return
	}
	name := strings.TrimSpace(e.name)
	if name == "" || e.op == editRename && name == path.Base(e.oldRel) {
		return
	}
	// A new entry's name may name folders too; a renamed one's not.
	parts := strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' })
	bad := len(parts) == 0 || e.op == editRename && len(parts) > 1
	for _, p := range parts {
		bad = bad || p == "." || p == ".."
	}
	if bad {
		a.toast(locales.FilesBadName(name))
		return
	}
	h := a.hostOf(e.pane)
	cl := h.cl
	if cl == nil {
		return
	}
	to := e.dir
	for _, p := range parts {
		to = h.joinPath(to, p)
	}
	rel := path.Join(append([]string{e.rel}, parts...)...)
	go func() {
		var err error
		if e.op == editRename {
			err = cl.Call("fs.rename", proto.FSRenameParams{From: e.path, To: to}, nil)
		} else {
			err = cl.Call("fs.create", proto.FSCreateParams{Path: to, Dir: e.op == editNewFolder}, nil)
		}
		a.update(func() {
			if err != nil {
				msg := locales.FilesCreateFailed(name, err.Error())
				if e.op == editRename {
					msg = locales.FilesRenameFailed(path.Base(e.oldRel), err.Error())
				}
				a.toast(msg)
				return
			}
			fs.gen++ // the folders list anew
			switch e.op {
			case editRename:
				a.renamed(h, e.path, to, e.oldRel, rel)
			case editNewFolder:
				fs.open[filesKey(h, to)] = true
			default:
				// A new file opens, as the user is to write it.
				if s := a.session(e.session); s != nil {
					a.askToSave(a.fileTargets(a.side.views[s.ID]), func() {
						if s := a.session(e.session); s != nil {
							a.openView(s, sideFiles, &fileView{pane: e.pane, path: to, rel: rel})
							a.focusViewer()
						}
					})
				}
			}
		})
	}()
}

// fileTargets is the file of a viewer with edits to save, if any.
func (a *App) fileTargets(v *sideView) []editTarget {
	if v == nil || v.file == nil {
		return nil
	}
	if t := a.fileTarget(v.file); t.dirty() {
		return []editTarget{t}
	}
	return nil
}

// renamed follows an entry of a host renamed, from to to: the files the
// viewers show in it, edits and all, and its folders unfolded.
func (a *App) renamed(h *host, from, to, fromRel, toRel string) {
	moved := func(p string) (string, bool) {
		switch {
		case p == from:
			return to, true
		case strings.HasPrefix(p, from) && strings.ContainsAny(p[len(from):len(from)+1], `/\`):
			return to + p[len(from):], true
		}
		return p, false
	}
	for _, v := range a.side.views {
		fv := v.file
		if fv == nil || a.hostOf(fv.pane) != h {
			continue
		}
		if p, ok := moved(fv.path); ok {
			fv.path = p
			fv.rel = toRel + strings.TrimPrefix(fv.rel, fromRel)
		}
	}
	fs := &a.side.files
	prefix := filesKey(h, "")
	for k := range fs.open {
		if p, ok := moved(strings.TrimPrefix(k, prefix)); ok && strings.HasPrefix(k, prefix) {
			delete(fs.open, k)
			fs.open[filesKey(h, p)] = true
		}
	}
}

// askDelete asks to delete a file or folder: to the trash for one of this
// machine, for good for a remote host's.
func (a *App) askDelete(pane string, it fileItem) {
	h := a.hostOf(pane)
	title := locales.FilesDeleteFileTitle(it.Name)
	if it.Dir {
		title = locales.FilesDeleteFolderTitle(it.Name)
	}
	detail, action := locales.FilesDeleteTrashDetail(), locales.FilesMoveToTrash()
	if runtime.GOOS == "windows" {
		detail, action = locales.FilesDeleteRecycleDetail(), locales.FilesMoveToRecycleBin()
	}
	if h.remote() {
		detail, action = locales.FilesDeleteForGoodDetail(), locales.FilesDelete()
	}
	a.confirm = &confirmation{
		open:    true,
		title:   title,
		detail:  detail,
		action:  action,
		destroy: true,
		run:     func() { a.deleteEntry(h, it) },
	}
}

// deleteEntry deletes a file or folder, as askDelete says.
func (a *App) deleteEntry(h *host, it fileItem) {
	cl := h.cl
	if cl == nil && h.remote() {
		return
	}
	go func() {
		var err error
		if h.remote() {
			err = cl.Call("fs.delete", proto.FSDeleteParams{Path: it.path}, nil)
		} else {
			err = mygo.Shell.TrashItem(it.path)
		}
		a.update(func() {
			if err != nil {
				a.toast(locales.FilesDeleteFailed(it.Name, err.Error()))
				return
			}
			a.side.files.gen++
			// A file a viewer shows is gone: read anew, it says so.
			for _, v := range a.side.views {
				if fv := v.file; fv != nil && a.hostOf(fv.pane) == h && (fv.path == it.path || withinDir(fv.path, it.path)) {
					fv.fetched = time.Time{}
				}
			}
		})
	}()
}

// entryMenu is a row's menu: a new file or folder in its folder, the
// folder's own for a folder, and the row's rename and delete.
func (a *App) entryMenu(m *ui.Menu, s *proto.Session, pane string, it fileItem) {
	dir, rel := it.parent, it.parentRel
	if it.Dir {
		dir, rel = it.path, it.rel
	}
	if m.Item(locales.FilesNewFile()).Chosen() {
		a.startCreate(s, pane, dir, rel, false)
	}
	if m.Item(locales.FilesNewFolder()).Chosen() {
		a.startCreate(s, pane, dir, rel, true)
	}
	m.Separator()
	if m.Item(locales.FilesRename()).Chosen() {
		a.startRenameEntry(s, pane, it)
	}
	if m.Item(locales.FilesDeleteItem()).Chosen() {
		a.askDelete(pane, it)
	}
	a.copyMenu(m, a.hostOf(pane), it.path)
}
