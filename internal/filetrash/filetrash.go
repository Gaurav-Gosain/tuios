// Package filetrash puts files in the freedesktop.org home trash and takes
// them out again. The client's Files view and the daemon's file-trash verb
// use it, so a file trashed on another machine goes where that machine's
// desktop trash shows it.
package filetrash

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/adrg/xdg"
)

// # Where a deleted file goes
//
// yeetui deletes with os.RemoveAll and nothing comes back. That is a fair
// bargain in a file manager somebody opened on purpose, with the whole screen
// and a cursor they have been steering. A rail is not that. It sits beside a
// terminal the whole time the terminal is being used, the cursor lands on it in
// passing, and a keystroke aimed at a pane can arrive here. So the default is
// the trash and permanent delete is the alternative you ask for.
//
// # Why the spec and not gio
//
// The freedesktop.org Trash specification is a directory layout and two text
// lines, and this implements it directly rather than running `gio trash`.
//
// gio is the desktop's own implementation and handles more cases than this
// does. It is also a process that has to be on the machine, and tuios runs on
// machines where it is not: a container, a build box, the far end of an ssh
// session. A default that works on the maintainer's laptop and refuses on the
// server is not a default. Running it also means one spawn per delete and an
// exit status where an error should be, and it cannot be tested without
// installing it.
//
// The layout is the same either way, so a file this puts in the trash is the
// file the desktop's own trash shows, restorable from it by the usual means.
//
// # What it does not do
//
// One trash: the home one, under XDG_DATA_HOME. The spec also describes a
// per-volume trash for files on other filesystems, and this does not implement
// it. A file on another disk cannot be renamed into the home trash, so the
// delete refuses and says to use permanent delete instead, which is on a key of
// its own. Copying the file into the home trash instead is the other option
// and is worse: a delete that silently duplicates a large tree onto the home
// disk is not what anybody pressed.
//
// And there is no trash on Windows. The Recycle Bin is not this layout, and a
// folder called Trash under AppData that no Windows tool can restore from would
// be a promise this cannot keep. trashAvailable says so, and the confirmation
// there says the delete is permanent, because it is.

// DirFunc resolves the home trash directory. It is a variable so a test can
// point it at a temporary tree, which is the only way to exercise the real
// rename without putting anything in the user's own trash.
var DirFunc = homeTrashDir

// homeTrashDir is $XDG_DATA_HOME/Trash, the spec's home trash.
func homeTrashDir() (string, error) {
	if !Available() {
		return "", ErrNoTrash
	}
	if xdg.DataHome == "" {
		return "", errors.New("no data directory")
	}
	return filepath.Join(xdg.DataHome, "Trash"), nil
}

// ErrNoTrash is the error of a system with no trash tuios can use.
var ErrNoTrash = errors.New("no trash on this system")

// Available reports whether this system has a trash tuios can put a file
// in. Windows does not, and a delete there is permanent and says so.
func Available() bool { return runtime.GOOS != "windows" }

// Put moves every path to the trash and reports how many went.
//
// A partial failure keeps going and reports the first error. One file in a set
// that is on another disk, or in a folder the user cannot write to, must not
// cost the rest of the set.
func Put(paths []string, now time.Time) (int, error) {
	dir, err := DirFunc()
	if err != nil {
		return 0, err
	}
	files := filepath.Join(dir, "files")
	info := filepath.Join(dir, "info")
	// 0700 is what the spec asks for. The trash holds whatever the user deleted,
	// which is as private as the files were.
	if err := os.MkdirAll(files, 0o700); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(info, 0o700); err != nil {
		return 0, err
	}

	ok := 0
	var firstErr error
	for _, p := range paths {
		if _, err := trashOne(files, info, p, now); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		ok++
	}
	return ok, firstErr
}

// Item is one path the trash took: where it was, and where it is in the
// trash, for an undo.
type Item struct {
	From string `json:"from"`
	File string `json:"file"`
	Info string `json:"info"`
}

// PutUndoable is Put that answers where each path went.
func PutUndoable(paths []string, now time.Time) ([]Item, error) {
	dir, err := DirFunc()
	if err != nil {
		return nil, err
	}
	files := filepath.Join(dir, "files")
	info := filepath.Join(dir, "info")
	if err := os.MkdirAll(files, 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(info, 0o700); err != nil {
		return nil, err
	}
	var out []Item
	var firstErr error
	for _, p := range paths {
		name, err := trashOne(files, info, p, now)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		abs, _ := filepath.Abs(p)
		out = append(out, Item{From: abs, File: filepath.Join(files, name), Info: filepath.Join(info, name+".trashinfo")})
	}
	return out, firstErr
}

// trashOne puts one path in the trash.
//
// The info file is written first, with O_EXCL. That is the spec's own locking:
// creating info/NAME.trashinfo exclusively is what claims NAME, so two deletes
// of two different files with the same base name cannot land on each other, and
// a name already claimed is found before anything has moved.
//
// The move itself is a rename, so it is atomic and costs nothing for a large
// tree. A rename across filesystems cannot work, and that is the one case this
// hands back to the caller with EXDEV intact so the message can name the fix.
func trashOne(filesDir, infoDir, path string, now time.Time) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(abs); err != nil {
		return "", err
	}
	base := filepath.Base(abs)
	if base == "" || base == "." || base == string(os.PathSeparator) {
		return "", errfmt.Errorf("cannot trash %s", path)
	}

	name, infoFile, err := claimTrashName(infoDir, base)
	if err != nil {
		return "", err
	}
	body := "[Trash Info]\nPath=" + trashInfoPath(abs) +
		"\nDeletionDate=" + now.Format("2006-01-02T15:04:05") + "\n"
	if _, err := infoFile.WriteString(body); err != nil {
		_ = infoFile.Close()
		_ = os.Remove(filepath.Join(infoDir, name+".trashinfo"))
		return "", err
	}
	if err := infoFile.Close(); err != nil {
		_ = os.Remove(filepath.Join(infoDir, name+".trashinfo"))
		return "", err
	}

	if err := os.Rename(abs, filepath.Join(filesDir, name)); err != nil {
		// The claim is given back, or the trash keeps an entry for a file that
		// is still where it was and a restore would fail on it later.
		_ = os.Remove(filepath.Join(infoDir, name+".trashinfo"))
		return "", err
	}
	return name, nil
}

// claimTrashName exclusively creates info/NAME.trashinfo for the first free
// NAME derived from base, and hands back the open file.
func claimTrashName(infoDir, base string) (string, *os.File, error) {
	ext := filepath.Ext(base)
	stem := base[:len(base)-len(ext)]
	for i := range 1000 {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s.%d%s", stem, i, ext)
		}
		f, err := os.OpenFile(filepath.Join(infoDir, name+".trashinfo"),
			os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			return name, f, nil
		}
		if !os.IsExist(err) {
			return "", nil, err
		}
	}
	return "", nil, errfmt.Errorf("%s: the trash already holds 1000 of these", base)
}

// trashInfoPath percent-encodes an absolute path for the Path line, which the
// spec defines as a URI path component.
//
// A file name off a disk can hold a space, a percent sign, a newline or a
// quote, and the info file is a text format that is parsed by line. Encoding is
// what keeps such a name from writing a second line of its own into the file
// that says where to restore it.
func trashInfoPath(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			b.WriteByte(c)
		case c == '-' || c == '_' || c == '.' || c == '~' || c == '/':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// Restore puts the file a Put took back where it was. file is the path in
// the trash, and to the path it came from: the trash's info file must name
// it, so a call cannot move any other file of the trash, or put one
// somewhere else. A path that is there now is left as it is.
func Restore(file, to string) error {
	dir, err := DirFunc()
	if err != nil {
		return err
	}
	files := filepath.Join(dir, "files")
	name := filepath.Base(file)
	if filepath.Clean(file) != filepath.Join(files, name) || name == "." || name == string(os.PathSeparator) {
		return errfmt.Errorf("%s is not in the trash", file)
	}
	info := filepath.Join(dir, "info", name+".trashinfo")
	body, err := os.ReadFile(info)
	if err != nil {
		return errfmt.Errorf("the trash has no record of %s", name)
	}
	if !strings.Contains(string(body), "\nPath="+trashInfoPath(filepath.Clean(to))+"\n") {
		return errfmt.Errorf("the trash has %s from another place", name)
	}
	if _, err := os.Lstat(to); err == nil {
		return os.ErrExist
	}
	if err := os.Rename(file, to); err != nil {
		return err
	}
	_ = os.Remove(info)
	return nil
}
