package vt

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
)

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

func kittyErrText(err error) string {
	if pathErr, ok := errors.AsType[*fs.PathError](err); ok {
		err = pathErr.Err
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == ';' {
			return ' '
		}
		return r
	}, err.Error())
}
