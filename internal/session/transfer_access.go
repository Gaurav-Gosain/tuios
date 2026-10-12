package session

import (
	"errors"
	"io/fs"
	"os"
)

// Who a copy reads and writes this machine's files for.
//
// A copy the person starts reads and writes as the person. A copy a linked
// machine starts is held to that machine's link policy (cs, the job's gate):
// the read deny list, the write roots and the write limits. A copy a pane
// starts with the files grant and no admin is held to the pane's folder
// (pane): every read and write on this machine goes through an os.Root at
// that folder, so a link the pane made inside its folder cannot carry a
// file in or out of it. A check of the top path when the copy starts is not
// enough: a folder copy writes and reads many paths below it, and a file can
// be swapped for a link after the walk listed it.
type access struct {
	cs   *connState
	pane string
}

// paneRefused is the error a read or write outside the pane's folder gets.
func paneRefused(path, pane string) *verbError {
	return hintedVerbError(ErrVerbForbidden, echoName(path)+" is outside the pane's folder "+echoName(pane)+", and the files grant reaches only that.", &VerbHint{
		Detail: "Nothing was read or written there. A link inside the folder does not reach out of it.",
	})
}

// paneFS is an os.Root at the pane's folder.
func (a access) paneFS() (fileFS, *verbError) {
	dir := a.pane
	// The folder was resolved when the grant was checked. One replaced by a
	// link since then is refused: a root opened through it would hold the
	// copy to wherever the link points.
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() || realPath(dir) != dir {
		return fileFS{}, paneRefused(dir, a.pane)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return fileFS{}, fileError("open", a.pane, err)
	}
	return fileFS{root: r, dir: dir}, nil
}

// checkRead refuses a read of path that a linked machine or a pane may not
// make.
func (d *Daemon) accCheckRead(a access, path string) *verbError {
	if a.pane != "" {
		if !pathUnder(realPath(path), a.pane) {
			return paneRefused(path, a.pane)
		}
		return nil
	}
	return d.checkLinkRead(a.cs, path)
}

// accOpenRead opens a regular file to read it. For a pane the open goes
// through the root, so a file swapped for a link that leaves the folder is
// refused at the open, not only at a check before it.
func (d *Daemon) accOpenRead(a access, path string) (*os.File, fs.FileInfo, *verbError, error) {
	if a.pane == "" {
		return d.openLinkRead(a.cs, path)
	}
	if verr := d.accCheckRead(a, path); verr != nil {
		return nil, nil, verr, nil
	}
	fsys, verr := a.paneFS()
	if verr != nil {
		return nil, nil, verr, nil
	}
	defer fsys.Close()
	f, fi, err := openRegularIn(fsys, fsys.confine(path))
	if err != nil && (errors.Is(err, fs.ErrPermission) || isRootEscape(err)) {
		return nil, nil, paneRefused(path, a.pane), nil
	}
	return f, fi, nil, err
}

// accWalkRoot is the folder a walk lists, judged by the read rules.
func (d *Daemon) accWalkRoot(a access, path string) (string, *verbError) {
	if a.pane == "" {
		return d.linkWalkRoot(a.cs, path)
	}
	if verr := d.accCheckRead(a, path); verr != nil {
		return "", verr
	}
	return realPath(path), nil
}

// accWriteFS is where a write of paths goes: for a pane an os.Root at its
// folder, for a link the link's root, else the plain file system.
func (d *Daemon) accWriteFS(a access, paths ...string) (fileFS, *verbError) {
	if a.pane == "" {
		return d.linkWriteFS(a.cs, paths...)
	}
	dir := a.pane
	for _, p := range paths {
		if !pathUnder(realParent(p), dir) {
			return fileFS{}, paneRefused(p, a.pane)
		}
	}
	return a.paneFS()
}

// isRootEscape reports whether err is an os.Root refusing a path that leaves
// it, through a link or "..".
func isRootEscape(err error) bool {
	var pe *fs.PathError
	return errors.As(err, &pe) && pe.Err != nil && pe.Err.Error() == "path escapes from parent"
}
