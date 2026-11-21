package session

import (
	"errors"
	"io/fs"
	"os"
)

type access struct {
	cs   *connState
	pane string
}

func paneRefused(path, pane string) *verbError {
	return hintedVerbError(ErrVerbForbidden, echoName(path)+" is outside the pane's folder "+echoName(pane)+", and the files grant reaches only that.", &VerbHint{
		Detail: "Nothing was read or written there. A link inside the folder does not reach out of it.",
	})
}

func (a access) paneFS() (fileFS, *verbError) {
	dir := a.pane
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() || realPath(dir) != dir {
		return fileFS{}, paneRefused(dir, a.pane)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return fileFS{}, fileError("open", a.pane, err)
	}
	return fileFS{root: r, dir: dir}, nil
}

func (d *Daemon) accCheckRead(a access, path string) *verbError {
	if a.pane != "" {
		if !pathUnder(realPath(path), a.pane) {
			return paneRefused(path, a.pane)
		}
		return nil
	}
	return d.checkLinkRead(a.cs, path)
}

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

func (d *Daemon) accWalkRoot(a access, path string) (string, *verbError) {
	if a.pane == "" {
		return d.linkWalkRoot(a.cs, path)
	}
	if verr := d.accCheckRead(a, path); verr != nil {
		return "", verr
	}
	return realPath(path), nil
}

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

func isRootEscape(err error) bool {
	var pe *fs.PathError
	return errors.As(err, &pe) && pe.Err != nil && pe.Err.Error() == "path escapes from parent"
}
