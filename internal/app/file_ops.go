package app

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

func createPath(cwd, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("empty path")
	}
	isDir := strings.HasSuffix(raw, "/") || strings.HasSuffix(raw, string(os.PathSeparator))
	clean, err := relativeUnder(strings.TrimRightFunc(raw, isPathSeparator))
	if err != nil {
		return "", err
	}
	full := filepath.Join(cwd, clean)
	if _, err := os.Lstat(full); err == nil {
		return "", errfmt.Errorf("already exists: %s", clean)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if isDir {
		if err := os.MkdirAll(full, 0o755); err != nil { //nolint:gosec // the user asked for this file or folder, so it gets ordinary permissions less the umask
			return "", err
		}
		return "New folder " + clean, nil
	}
	if parent := filepath.Dir(full); parent != cwd {
		if err := os.MkdirAll(parent, 0o755); err != nil { //nolint:gosec // the user asked for this file or folder, so it gets ordinary permissions less the umask
			return "", err
		}
	}
	f, err := os.OpenFile(full, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644) //nolint:gosec // the user asked for this file or folder, so it gets ordinary permissions less the umask
	if err != nil {
		return "", err
	}
	_ = f.Close()
	return "New file " + clean, nil
}

func renameEntry(cwd, oldName, newName string) (string, error) {
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return "", errors.New("new name is empty")
	}
	if newName == oldName {
		return "", errors.New("name unchanged")
	}
	clean, err := relativeUnder(newName)
	if err != nil {
		return "", err
	}
	src := filepath.Join(cwd, oldName)
	dst := filepath.Join(cwd, clean)
	if _, err := os.Lstat(src); err != nil {
		return "", err
	}
	if _, err := os.Lstat(dst); err == nil {
		return "", errfmt.Errorf("destination already exists: %s", clean)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if parent := filepath.Dir(dst); parent != cwd {
		if err := os.MkdirAll(parent, 0o755); err != nil { //nolint:gosec // the user asked for this file or folder, so it gets ordinary permissions less the umask
			return "", err
		}
	}
	if err := os.Rename(src, dst); err != nil {
		return "", err
	}
	return "Renamed " + oldName + " to " + clean, nil
}

func relativeUnder(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("empty path")
	}
	if filepath.IsAbs(raw) {
		return "", errors.New("absolute paths not allowed")
	}
	clean := filepath.Clean(raw)
	if clean == "." {
		return "", errors.New("empty path")
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", errors.New("path escapes the current directory")
	}
	return clean, nil
}

func deletePaths(paths []string) (int, error) {
	ok := 0
	var firstErr error
	for _, p := range paths {
		if err := os.RemoveAll(p); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		ok++
	}
	return ok, firstErr
}

func copyTree(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	case info.IsDir():
		if err := os.Mkdir(dst, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	case info.Mode().IsRegular():
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		defer func() { _ = out.Close() }()
		_, err = io.Copy(out, in)
		return err
	default:
		return errfmt.Errorf("cannot copy special file: %s", src)
	}
}

type fileClipboard struct {
	Paths []string
	Move  bool
}

func (c fileClipboard) Empty() bool { return len(c.Paths) == 0 }

func pastePaths(cwd string, clip fileClipboard) (done, renamed int, err error) {
	if clip.Empty() {
		return 0, 0, errors.New("clipboard is empty")
	}
	var firstErr error
	for _, src := range clip.Paths {
		if perr := pasteOne(cwd, src, clip.Move, &renamed); perr != nil {
			if firstErr == nil {
				firstErr = perr
			}
			continue
		}
		done++
	}
	return done, renamed, firstErr
}

func pasteOne(cwd, src string, move bool, renamed *int) error {
	if withinTree(src, cwd) {
		return errfmt.Errorf("%s: cannot paste a folder into itself", filepath.Base(src))
	}
	dst := filepath.Join(cwd, filepath.Base(src))
	unique, err := uniqueLocalDst(dst)
	if err != nil {
		return err
	}
	if unique != dst {
		*renamed++
		dst = unique
	}
	if !move {
		return copyTree(src, dst)
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := copyTree(src, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

func withinTree(src, dir string) bool {
	src = filepath.Clean(src)
	dir = filepath.Clean(dir)
	if src == dir {
		return true
	}
	return strings.HasPrefix(dir, src+string(os.PathSeparator))
}

func uniqueLocalDst(dst string) (string, error) {
	if _, err := os.Lstat(dst); err != nil {
		if os.IsNotExist(err) {
			return dst, nil
		}
		return "", err
	}
	ext := filepath.Ext(dst)
	stem := dst[:len(dst)-len(ext)]
	for i := 1; i < 1000; i++ {
		cand := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		if _, err := os.Lstat(cand); err != nil {
			if os.IsNotExist(err) {
				return cand, nil
			}
			return "", err
		}
	}
	return "", errfmt.Errorf("%s: 1000 renames exhausted", dst)
}

func fileOpError(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "That file is gone. The list is out of date."
	case errors.Is(err, fs.ErrPermission):
		return "You can not write to that folder."
	case errors.Is(err, syscall.EROFS):
		return "That disk is read only."
	case errors.Is(err, syscall.ENOSPC):
		return "That disk is full."
	case errors.Is(err, syscall.EXDEV):
		return "That file is on another disk. Use permanent delete."
	case errors.Is(err, fs.ErrExist):
		return "That name is already in use."
	}
	msg := err.Error()
	if strings.HasPrefix(msg, "already exists: ") {
		return "That name is already in use."
	}
	if strings.HasPrefix(msg, "destination already exists: ") {
		return "That name is already in use."
	}
	switch msg {
	case "empty path", "new name is empty":
		return "Type a name."
	case "name unchanged":
		return "That is the same name."
	case "absolute paths not allowed":
		return "Type a name, not a full path."
	case "path escapes the current directory":
		return "That name goes outside this folder."
	case "clipboard is empty":
		return "Copy or cut a file first."
	}
	if strings.Contains(msg, "cannot paste a folder into itself") {
		return "You can not paste a folder into itself."
	}
	if strings.Contains(msg, "cannot copy special file") {
		return "tuios can not copy that kind of file."
	}
	return "That did not work. " + capitalizeFirst(msg)
}

func capitalizeFirst(s string) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	if r[0] >= 'a' && r[0] <= 'z' {
		r[0] -= 'a' - 'A'
	}
	return string(r) + "."
}

func isPathSeparator(r rune) bool {
	return r == '/' || r == os.PathSeparator
}
