package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
)

// The viewer's edits not saved outlast the app, as VS Code's hot exit
// keeps them: quitting asks nothing, and the next launch shows them as it
// left them, not saved still. The app writes them, with what each
// session's viewer showed, to unsaved.json, a moment after the user
// pauses typing, so that a crash loses no more than that, and as it
// quits. An edit comes back as its file's text loads, in Files or in a
// diff of Changes, and with the hash of the file it was made on, so that
// saving it over a file changed meanwhile asks first (askOverwrite);
// until then it stays in the file.

// backupDelay is how long the user pauses typing before their edits are
// backed up.
const backupDelay = time.Second

// viewerBackup is unsaved.json: what each session's viewer shows, and the
// edits not saved.
type viewerBackup struct {
	Views   map[string]viewBackup `json:"views,omitempty"`
	Unsaved []unsavedText         `json:"unsaved,omitempty"`
}

// viewBackup is what a session's viewer shows: the diffs of Changes, or a
// file of Files.
type viewBackup struct {
	Kind string `json:"kind"`
	Pane string `json:"pane,omitempty"` // the file's
	Path string `json:"path,omitempty"`
	Rel  string `json:"rel,omitempty"`
}

// unsavedText is a text the user edited and didn't save: a file of
// Files, or the new side of a diff of Changes (Kind), as its file has it,
// line endings too, and the hash of the file it was made on.
type unsavedText struct {
	Kind string `json:"kind"`
	Host string `json:"host,omitempty"` // "" for this machine
	Pane string `json:"pane,omitempty"`
	Path string `json:"path"`
	Hash string `json:"hash"`
	Text string `json:"text"`
}

func (u unsavedText) key() string { return unsavedKey(u.Kind, u.Host, u.Path) }

func unsavedKey(kind, host, path string) string { return kind + "\x00" + host + "\x00" + path }

func backupPath() string { return filepath.Join(proto.Dir(), "unsaved.json") }

// backupState is what the app keeps of its backups.
type backupState struct {
	// unsaved are the edits backed up that no text shows yet, by kind,
	// host and path (unsavedKey).
	unsaved map[string]unsavedText
	// sig is what the backup is of, as last seen, changed when it
	// changed; written what the file has.
	sig, written string
	changed      time.Time
}

// loadBackup reads the viewers and the edits the app left as it last
// quit.
func (a *App) loadBackup() {
	b, err := os.ReadFile(backupPath())
	if err != nil {
		return
	}
	var vb viewerBackup
	if json.Unmarshal(b, &vb) != nil {
		return
	}
	a.side.backup.unsaved = map[string]unsavedText{}
	for _, u := range vb.Unsaved {
		a.side.backup.unsaved[u.key()] = u
	}
	for sid, v := range vb.Views {
		if v.Kind != sideChanges && v.Kind != sideFiles {
			continue
		}
		if a.side.views == nil {
			a.side.views = map[string]*sideView{}
		}
		sv := &sideView{kind: v.Kind}
		if v.Path != "" {
			sv.file = &fileView{pane: v.Pane, path: v.Path, rel: v.Rel}
		}
		if v.Kind == sideFiles && sv.file == nil {
			continue
		}
		a.side.views[sid] = sv
	}
}

// restoreEdit puts back in b, a text just loaded, the edit backed up of
// its file, if any, with the hash of the file it was made on.
func (a *App) restoreEdit(kind string, h *host, path string, b *editor.Buffer, hash *string) {
	key := unsavedKey(kind, h.name, path)
	u, ok := a.side.backup.unsaved[key]
	if !ok {
		return
	}
	delete(a.side.backup.unsaved, key)
	replaceText(b, u.Text)
	*hash = u.Hash
}

// backupNow is the backup of the viewers as they are.
func (a *App) backupNow() viewerBackup {
	vb := viewerBackup{Views: map[string]viewBackup{}}
	for sid, v := range a.side.views {
		if v.kind == "" {
			continue
		}
		b := viewBackup{Kind: v.kind}
		if f := v.file; f != nil {
			b.Pane, b.Path, b.Rel = f.pane, f.path, f.rel
		}
		vb.Views[sid] = b
	}
	for _, t := range a.allTargets() {
		vb.Unsaved = append(vb.Unsaved, unsavedText{Kind: t.kind, Host: a.hostOf(t.pane).name, Pane: t.pane, Path: t.path, Hash: *t.hash, Text: bufferText(t.buf)})
	}
	for _, u := range a.side.backup.unsaved {
		vb.Unsaved = append(vb.Unsaved, u)
	}
	slices.SortFunc(vb.Unsaved, func(x, y unsavedText) int { return strings.Compare(x.key(), y.key()) })
	return vb
}

// backupSig is what the backup is of, cheaply: each viewer's, and each
// text's with edits, at its version.
func (a *App) backupSig() string {
	var b strings.Builder
	for sid, v := range a.side.views {
		if v.kind != "" {
			fmt.Fprint(&b, sid, v.kind)
			if v.file != nil {
				b.WriteString(v.file.path)
			}
			b.WriteByte(0)
		}
	}
	for _, t := range a.allTargets() {
		fmt.Fprint(&b, t.kind, t.path, t.buf.Version(), *t.hash, "\x00")
	}
	fmt.Fprint(&b, len(a.side.backup.unsaved))
	return b.String()
}

// backupEdits writes the backup once what it is of changed, and the user
// paused for backupDelay.
func (a *App) backupEdits(c *ui.Context) {
	bs := &a.side.backup
	sig := a.backupSig()
	if sig != bs.sig {
		bs.sig, bs.changed = sig, c.Now()
	}
	if sig == bs.written {
		return
	}
	if wait := backupDelay - c.Now().Sub(bs.changed); wait > 0 {
		c.After(wait)
		return
	}
	bs.written = sig
	writeBackup(a.backupNow())
}

// writeBackup writes the backup: small, the texts the user edited alone,
// and written as they pause, so at once.
func writeBackup(vb viewerBackup) {
	path := backupPath()
	if len(vb.Views) == 0 && len(vb.Unsaved) == 0 {
		os.Remove(path)
		return
	}
	b, err := json.Marshal(vb)
	if err != nil {
		return
	}
	// Private: it holds what the user typed.
	os.MkdirAll(proto.Dir(), 0o700)
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		os.Rename(tmp, path)
	}
}

// backupOnQuit writes the backup as the app quits.
func (a *App) backupOnQuit() {
	writeBackup(a.backupNow())
}
