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
)

// # The file operations
//
// sidebar_files.go said the section "does not create, rename, delete or move
// anything: yeetui does all of that far better than twenty-eight columns ever
// will". It does the basics now, and the paragraph is wrong in one direction
// only: yeetui is still the file manager, and this is still a rail. What moved
// across is the set the maintainer named, in his own words and with his own
// rules, so a person who knows one knows the other.
//
// The functions here are yeetui's, ported: createPath, renameEntry, copyTree,
// pastePaths and uniqueLocalDst keep their names, their collision rules and
// their refusals. Two things differ, and both are about where the rail sits.
//
// A delete goes to the trash. yeetui removes with os.RemoveAll, which is
// defensible in a file manager somebody opened on purpose; a rail is beside a
// terminal and is a more incidental place to put a keystroke. file_trash.go
// says how the trash works and what it costs.
//
// And nothing here runs on the update goroutine. Every one of these is called
// from inside a tea.Cmd, off the loop, for the reason at the top of
// sidebar_files.go: a copy of a large tree on the loop would freeze every pane
// in the client for as long as the copy took.
//
// # No shell, anywhere
//
// Not one of these builds a command line. A name off a filesystem can hold a
// space, a quote, a semicolon or a newline, and every one of those is inert
// here because it is an argument to a syscall and never a word in a sentence
// somebody else parses. sidebar_files.go's shellQuote exists for the one place
// that does type at a prompt, and no operation here goes near it.

// createPath makes the file or folder raw names, under cwd.
//
// A trailing slash means a folder; anything else is a regular file. A name with
// a separator in it nests, and the folders on the way are made, which is
// yeetui's rule and the reason the prompt says "trailing / for a folder"
// rather than offering two prompts.
//
// It never overwrites. An existing target is an error, not a replacement.
func createPath(cwd, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("empty path")
	}
	isDir := strings.HasSuffix(raw, "/") || strings.HasSuffix(raw, string(os.PathSeparator))
	clean, err := relativeUnder(strings.TrimRight(raw, "/"+string(os.PathSeparator)))
	if err != nil {
		return "", err
	}
	full := filepath.Join(cwd, clean)
	if _, err := os.Lstat(full); err == nil {
		return "", fmt.Errorf("already exists: %s", clean)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if isDir {
		if err := os.MkdirAll(full, 0o755); err != nil {
			return "", err
		}
		return "New folder " + clean, nil
	}
	if parent := filepath.Dir(full); parent != cwd {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return "", err
		}
	}
	f, err := os.OpenFile(full, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	_ = f.Close()
	return "New file " + clean, nil
}

// renameEntry renames cwd/oldName to cwd/newName.
//
// The new name is taken as typed, so "sub/file.txt" moves the entry into a
// subfolder and makes the subfolder if it has to. That is createPath's rule and
// yeetui's, and one rule for both prompts is one rule to learn.
//
// It refuses an existing destination rather than replacing it. A rename is the
// one operation with no undo even with a trash, because the thing it would
// destroy is not the thing the user named.
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
	// The listing the prompt was opened over can be older than the disk. A
	// target that went away between the listing and the enter is an ordinary
	// event, and it has to read as "that file is gone" rather than as the
	// rename's own failure.
	if _, err := os.Lstat(src); err != nil {
		return "", err
	}
	if _, err := os.Lstat(dst); err == nil {
		return "", fmt.Errorf("destination already exists: %s", clean)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if parent := filepath.Dir(dst); parent != cwd {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return "", err
		}
	}
	if err := os.Rename(src, dst); err != nil {
		return "", err
	}
	return "Renamed " + oldName + " to " + clean, nil
}

// relativeUnder cleans a typed name and refuses one that would leave cwd.
//
// It is the guard on both prompts, and it is deliberately about what the string
// says rather than about what the filesystem does with it. An absolute path is
// refused because the prompt is opened over one folder and the user is naming
// something in it. A name that cleans to ".." or starts with "../" is refused
// because filepath.Clean has already folded every embedded segment, so one
// check catches both "../x" and "a/../../x".
//
// What it does not do is resolve symlinks. A folder in the listing that points
// somewhere else takes a nested create there, exactly as it would at a shell
// prompt in the same directory, and the name the user typed is the name they
// saw on the row.
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

// deletePaths removes every path for good, folders and all.
//
// It is the explicit alternative to the trash and never the default. A partial
// failure still reports how many went, plus the first thing that stopped it,
// because a batch that half worked has to say which half.
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

// copyTree copies the file or folder tree at src to dst, which must not exist.
//
// Symlinks are recreated as symlinks rather than followed, so copying a folder
// does not silently duplicate whatever it points at. Devices, sockets and fifos
// are refused by name: there is no useful copy of one, and a reader that
// blocked on a fifo would hang the command holding it.
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
		return fmt.Errorf("cannot copy special file: %s", src)
	}
}

// fileClipboard is what copy and cut captured. Absolute paths, because the
// listing can walk somewhere else before the paste.
type fileClipboard struct {
	Paths []string
	// Move is true when the set was cut. The source goes only after the
	// destination is written.
	Move bool
}

// Empty reports whether there is nothing to paste.
func (c fileClipboard) Empty() bool { return len(c.Paths) == 0 }

// pastePaths puts every path in clip into cwd, copying or moving.
//
// Nothing is overwritten, ever. A name already taken in cwd gets the first free
// "name (N).ext" instead, which is yeetui's rule: the user asked for the file
// to be here, they did not ask for the one already here to go. That is also why
// a paste raises no confirmation. There is no destructive branch in it to
// confirm.
//
// A partial failure keeps going and reports the first error, so one unreadable
// name in a set of ten does not cost the other nine.
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

// pasteOne places one source path into cwd.
func pasteOne(cwd, src string, move bool, renamed *int) error {
	// A folder cannot be moved or copied into itself or into its own subtree:
	// the move fails halfway on some kernels and the copy recurses for as long
	// as the disk lasts. Refused by name rather than discovered.
	if withinTree(src, cwd) {
		return fmt.Errorf("%s: cannot paste a folder into itself", filepath.Base(src))
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
	// Another filesystem. Rename cannot cross one, so the move is a copy and
	// then a remove, and the remove happens only once the copy has finished.
	if err := copyTree(src, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

// withinTree reports whether dir is src itself or anywhere under it.
func withinTree(src, dir string) bool {
	src = filepath.Clean(src)
	dir = filepath.Clean(dir)
	if src == dir {
		return true
	}
	return strings.HasPrefix(dir, src+string(os.PathSeparator))
}

// uniqueLocalDst is dst when nothing is there, and otherwise the first free
// "stem (N).ext". It is what keeps a paste from ever losing a file.
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
	return "", fmt.Errorf("%s: 1000 renames exhausted", dst)
}

// fileOpError turns a failure into one short sentence for the rail.
//
// Every one of these is an ordinary event rather than a fault: a folder the
// user cannot write to, a disk mounted read only, a file something else removed
// between the listing and the keypress. Each says what happened, and where
// there is something to do next it says that too.
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
	// The messages the operations above raise themselves are already sentences
	// a person can read, so they come through as written.
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

// capitalizeFirst makes a sentence out of an error string.
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
