package main

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/Gaurav-Gosain/tuios/internal/guios/git"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The viewer's texts edit as a code editor's do, VS Code's and Zed's: a
// file of Files, and the new side of a diff of Changes. Edits stay in the
// app until the user saves them, ⌘S, as a dot on the viewer's close
// button and by a diff's file name says; closing the viewer, opening
// another file over one edited asks to save them first; quitting keeps
// them for the next launch (backup.go).
// kerod writes a file only as it was read: one changed meanwhile asks to
// overwrite it, or to discard the edits for the file as it is now. A text
// the user hasn't edited follows its file as it changes.

// edited is what the viewer keeps of a text the user edits.
type edited struct {
	saving bool
}

// editTarget is a text the user edits in the viewer, and its file.
type editTarget struct {
	kind   string // sideFiles, or sideChanges for a diff's
	name   string // as prompts say it
	pane   string // a pane of the host the file is on
	path   string // the file, on the host
	hash   *string
	buf    *editor.Buffer
	edits  *edited
	reload func() // reads the file anew, for the text shows it as it is
}

// dirty tells that the text has edits to save.
func (t editTarget) dirty() bool { return t.buf != nil && t.buf.Modified() }

// fileTarget is a file of Files the viewer shows.
func (a *App) fileTarget(fv *fileView) editTarget {
	t := editTarget{kind: sideFiles, name: pathBase(fv.rel), pane: fv.pane, path: fv.path, hash: &fv.hash, edits: &fv.edits,
		reload: func() { fv.fetched = time.Time{} }}
	if fv.st != nil {
		t.buf = fv.st.Buffer
	}
	return t
}

// diffTarget is the new side of a file's diff.
func (a *App) diffTarget(sf *sideFile) editTarget {
	t := editTarget{kind: sideChanges, name: pathBase(sf.f.Path), pane: sf.pane, path: a.hostOf(sf.pane).joinPath(sf.root, sf.f.Path),
		hash: &sf.hash, edits: &sf.edits, reload: func() { a.fetchFile(sf, "") }}
	if sf.d != nil && !sf.patch {
		t.buf = sf.d.New
	}
	return t
}

func pathBase(p string) string { return p[strings.LastIndexAny(p, `/\`)+1:] }

// viewTargets are the texts of a viewer with edits to save: its file, or
// its diffs.
func (a *App) viewTargets(v *sideView) []editTarget {
	var ts []editTarget
	if v.kind == sideFiles {
		if v.file != nil {
			if t := a.fileTarget(v.file); t.dirty() {
				ts = append(ts, t)
			}
		}
		return ts
	}
	for _, sf := range a.side.changes.files {
		if t := a.diffTarget(sf); t.dirty() {
			ts = append(ts, t)
		}
	}
	return ts
}

// allTargets are every text with edits to save, of every session's
// viewer.
func (a *App) allTargets() []editTarget {
	var ts []editTarget
	for _, v := range a.side.views {
		if v.file != nil {
			if t := a.fileTarget(v.file); t.dirty() {
				ts = append(ts, t)
			}
		}
	}
	for _, sf := range a.side.changes.files {
		if t := a.diffTarget(sf); t.dirty() {
			ts = append(ts, t)
		}
	}
	return ts
}

// saveFocused saves the text the viewer's keyboard is in: its file, or
// the diff last focused.
func (a *App) saveFocused(v *sideView) {
	var t editTarget
	if v.kind == sideFiles && v.file != nil {
		t = a.fileTarget(v.file)
	} else if sf := a.side.changes.focus; sf != nil {
		t = a.diffTarget(sf)
	}
	if t.dirty() && !t.edits.saving {
		a.saveTargets([]editTarget{t}, nil)
	}
}

// saveTargets saves texts, then runs then once every one is saved: not
// when one isn't, as its file changed meanwhile and the user cancels, or
// kerod couldn't write it.
func (a *App) saveTargets(ts []editTarget, then func()) {
	left, failed := len(ts), false
	for _, t := range ts {
		a.saveText(t, false, func(ok bool) {
			failed = failed || !ok
			if left--; left == 0 && !failed && then != nil {
				then()
			}
		})
	}
}

// saveText writes an edited text to its file, through kerod, which writes
// it only as it was read unless forced. One changed meanwhile asks to
// overwrite it, or to discard the edits; one kerod couldn't write keeps
// its edits, as a toast says. done reports whether it was saved.
func (a *App) saveText(t editTarget, force bool, done func(ok bool)) {
	cl := a.hostOf(t.pane).cl
	if cl == nil || t.buf == nil {
		done(false)
		return
	}
	text, v := bufferText(t.buf), t.buf.Version()
	t.edits.saving = true
	go func() {
		var written string
		err := cl.Call("fs.write", proto.FSWriteParams{Path: t.path, Hash: *t.hash, Text: text, Force: force}, &written)
		a.update(func() {
			t.edits.saving = false
			switch {
			case err != nil && strings.Contains(err.Error(), git.ErrChanged.Error()):
				a.askOverwrite(t, done)
			case err != nil:
				a.toast(locales.ViewerSaveFailed(t.name, err.Error()))
				done(false)
			default:
				*t.hash = written
				if t.buf.Version() == v {
					t.buf.MarkSaved()
				}
				done(true)
			}
		})
	}()
}

// askOverwrite asks what to do of a text whose file changed since it was
// read: overwrite it with the user's text, or discard the user's edits
// for the file as it is now.
func (a *App) askOverwrite(t editTarget, done func(ok bool)) {
	if a.confirm != nil {
		a.toast(locales.ViewerConflictTitle(t.name))
		done(false)
		return
	}
	a.confirm = &confirmation{
		open:    true,
		title:   locales.ViewerConflictTitle(t.name),
		detail:  locales.ViewerConflictDetail(),
		action:  locales.ViewerOverwrite(),
		destroy: true,
		run:     func() { a.saveText(t, true, done) },
		alt:     locales.ViewerDiscard(),
		altRun: func() {
			discard(t)
			done(false)
		},
		altDestroy: true,
	}
}

// discard drops a text's edits, for its file as it is now.
func discard(t editTarget) {
	t.buf.MarkSaved()
	t.reload()
}

// askToSave asks to save texts' edits before then, which saving them, or
// not, runs; Cancel doesn't.
func (a *App) askToSave(ts []editTarget, then func()) {
	if len(ts) == 0 {
		then()
		return
	}
	title := locales.ViewerSaveMany(len(ts))
	if len(ts) == 1 {
		title = locales.ViewerSaveOne(ts[0].name)
	}
	a.confirm = &confirmation{
		open:   true,
		title:  title,
		detail: locales.ViewerSaveDetail(),
		action: locales.ViewerSave(),
		run:    func() { a.saveTargets(ts, then) },
		alt:    locales.ViewerDontSave(),
		altRun: func() {
			for _, t := range ts {
				discard(t)
			}
			then()
		},
		altDestroy: true,
	}
}

// closeButton closes the viewer: a dot while it has edits to save, as an
// editor's tab shows, the cross as the pointer comes over it.
func closeButton(c *ui.Context, pal *palette, label string, dirty bool, fn func()) {
	if dirty {
		label = locales.ViewerUnsaved() + ". " + label
	}
	b := ui.ButtonBase(c.Key("close-side")).Size(hControl, 26).Radius(rRow).Center().Shrink(0).TextColor(pal.textMuted).Label(label).Tooltip(label)
	hovered := b.Hovered() || b.Pressed()
	if b.Pressed() {
		b.Background(pal.rowHover.Alpha(0.12))
	} else if hovered {
		b.Background(pal.rowHover).TextColor(pal.text)
	}
	b.Children(func() {
		if dirty && !hovered {
			ui.Box(c).Size(8, 8).Radius(4).Background(pal.textMuted)
			return
		}
		ui.Icon(c, closeIcon).Size(16, 16)
	})
	if b.Clicked() {
		fn()
	}
}

// bufferText is a buffer's text as its file has it, its line endings too.
func bufferText(b *editor.Buffer) string {
	var sb strings.Builder
	b.WriteTo(&sb)
	return sb.String()
}
