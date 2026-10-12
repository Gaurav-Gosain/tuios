package vt

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
)

// kittyUploadFileError checks that the file a t=f or t=t upload names can be
// read, and returns kitty's error for it, or "" when it can. It follows the
// rules the client's passthrough applies before it reads or forwards a path:
// an absolute path, symlinks followed, nothing under /proc, /sys or /dev, a
// regular file this user owns, and for t=t a name with the kitty marker in a
// temporary directory.
//
// The file is opened and closed, not read: the host terminal or the client
// reads it, and a temporary file is theirs to delete once read. The open
// itself cannot block on a FIFO swapped in after the path was resolved (see
// kittyOpenRegular). It can still wait on a network mount that does not
// answer, which is why the caller runs it off the emulator's lock.
func kittyUploadFileError(medium KittyGraphicsMedium, path string) string {
	if path == "" || !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return "EINVAL:the file path is not an absolute path"
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return kittyOpenError(err)
	}
	if KittySensitivePath(resolved) {
		return "EPERM:the file is in a place no image may be read from"
	}
	if medium == KittyMediumTempFile &&
		(!strings.Contains(filepath.Base(resolved), KittyTempMarker) || !KittyInTempDir(resolved)) {
		return "EPERM:a temporary file must be in a temporary directory and name tty-graphics-protocol"
	}
	if err := kittyOpenRegular(resolved); err != nil {
		return kittyOpenError(err)
	}
	return ""
}

// errKittyNotRegular is a path that is not a regular file this user owns.
var errKittyNotRegular = errors.New("not a regular file of this user")

func kittyOpenError(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "ENOENT:Failed to open file for graphics transmission: no such file"
	case errors.Is(err, errKittyNotRegular):
		return "EINVAL:the file is not a regular file of this user"
	}
	return "EBADF:Failed to open file for graphics transmission: " + kittyErrText(err)
}

// kittyErrText is an error's text without the path, which a reply has no
// room for and the guest already knows.
func kittyErrText(err error) string {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		err = pathErr.Err
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == ';' {
			return ' '
		}
		return r
	}, err.Error())
}
