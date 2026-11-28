package session

import (
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/filetrash"
)

// What a linked machine may do in a folder of this machine, and the trash
// over a link.
//
// The file-list reply over a link says whether the peer may write in the
// folder and where it may write, so the far side's Files view shows it
// before a write is tried, not after the refusal. It also leaves out the
// names the read deny list hides, and counts them.
//
// file-trash moves paths to this machine's trash for a peer that may write
// them, and file-untrash puts one back: only a file the trash took from that
// path, and only to a path the peer may write.

// ErrVerbNoTrash says the machine has no trash tuios can use, so a delete
// there is for good.
const ErrVerbNoTrash = "no_trash"

// linkAccess is what the machine at the other end of a link may do in one
// folder: files says the link allows files at all, write that it may write
// in this folder, and roots the folders it may write in, as ~ paths.
type linkAccess struct {
	Files bool     `json:"files"`
	Write bool     `json:"write"`
	Roots []string `json:"roots,omitempty"`
}

// linkAccessOf is the access of the link cs in dir.
func (d *Daemon) linkAccessOf(cs *connState, dir string) linkAccess {
	policy := d.linkPolicy(cs)
	if !policy.Allows(config.LinkAllowFiles) {
		return linkAccess{}
	}
	home, _ := d.linkWriteRoots(policy)
	a := linkAccess{Files: true}
	real := realPath(dir)
	denied := !plainLinkPath(dir) || linkDenied(real, home, d.linkPrivateDirs())
	for _, r := range linkRootPaths(policy) {
		a.Roots = append(a.Roots, tildePath(r, home))
		if !denied && pathUnder(real, r) {
			a.Write = true
		}
	}
	return a
}

// verbFileTrash moves paths to this machine's trash. Over a link each path
// must be one the peer may write. A path that fails does not stop the rest.
func (d *Daemon) verbFileTrash(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Paths []string `json:"paths"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if len(p.Paths) == 0 {
		return nil, invalidParam("paths", "name at least one path")
	}
	if !filetrash.Available() {
		return nil, newVerbError(ErrVerbNoTrash, "trash: this machine has no trash")
	}
	paths := make([]string, 0, len(p.Paths))
	for _, raw := range p.Paths {
		path, verr := expandPath(raw)
		if verr != nil {
			return nil, verr
		}
		fsys, verr := d.linkWriteFS(cs, path)
		if verr != nil {
			return nil, verr
		}
		fsys.Close()
		paths = append(paths, path)
	}
	got, err := filetrash.PutUndoable(paths, time.Now())
	out := map[string]any{"trashed": got}
	if err != nil {
		if len(got) == 0 {
			return nil, fileError("trash", paths[0], err)
		}
		out["error"] = fileError("trash", "", err).Message
	}
	return out, nil
}

// verbFileUntrash puts a file the trash took back where it was.
func (d *Daemon) verbFileUntrash(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		File string `json:"file"`
		To   string `json:"to"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	to, verr := expandPath(p.To)
	if verr != nil {
		return nil, verr
	}
	file, verr := expandPath(p.File)
	if verr != nil {
		return nil, verr
	}
	fsys, verr := d.linkWriteFS(cs, to)
	if verr != nil {
		return nil, verr
	}
	fsys.Close()
	if err := filetrash.Restore(file, to); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, newVerbError(ErrVerbFileExists, "untrash: "+echoName(to)+" is there now")
		}
		return nil, fileError("untrash", to, err)
	}
	return map[string]any{"path": to}, nil
}
