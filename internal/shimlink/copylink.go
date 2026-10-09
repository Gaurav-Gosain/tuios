package shimlink

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Method is how InstallFile made the program at the link's path.
type Method string

// The methods InstallFile tries, in this order.
const (
	// MethodHardlink is a second name for the binary. It needs no
	// privilege on Windows, but the link and the binary must be on one
	// volume.
	MethodHardlink Method = "hardlink"
	// MethodCopy is a copy of the binary, with the binary's size and
	// modification time, so a later start can tell that it is stale.
	MethodCopy Method = "copy"
	// MethodSymlink is a symbolic link. On Windows it needs Developer Mode
	// or an administrator, so it is the last resort there.
	MethodSymlink Method = "symlink"
)

// FS is the part of the filesystem InstallFile uses. OSFS is the real one;
// a test wraps it to make one operation fail, as a link across volumes or a
// copy onto a full disk does.
type FS interface {
	Stat(name string) (fs.FileInfo, error)
	Lstat(name string) (fs.FileInfo, error)
	Readlink(name string) (string, error)
	Link(oldname, newname string) error
	Symlink(oldname, newname string) error
	// Copy writes a copy of src at dst, which must not exist, and gives it
	// src's modification time.
	Copy(src, dst string) error
	Rename(oldname, newname string) error
	Remove(name string) error
	Glob(pattern string) ([]string, error)
	Open(name string) (io.ReadCloser, error)
	// Alive reports whether a process with this id runs.
	Alive(pid int) bool
}

// OSFS is FS on the real filesystem.
type OSFS struct{}

func (OSFS) Stat(name string) (fs.FileInfo, error)   { return os.Stat(name) }
func (OSFS) Lstat(name string) (fs.FileInfo, error)  { return os.Lstat(name) }
func (OSFS) Readlink(name string) (string, error)    { return os.Readlink(name) }
func (OSFS) Link(oldname, newname string) error      { return os.Link(oldname, newname) }
func (OSFS) Symlink(oldname, newname string) error   { return os.Symlink(oldname, newname) }
func (OSFS) Rename(oldname, newname string) error    { return os.Rename(oldname, newname) }
func (OSFS) Remove(name string) error                { return os.Remove(name) }
func (OSFS) Glob(pattern string) ([]string, error)   { return filepath.Glob(pattern) }
func (OSFS) Open(name string) (io.ReadCloser, error) { return os.Open(name) } //nolint:gosec // the tuios binary and its link
func (OSFS) Alive(pid int) bool                      { return processAlive(pid) }

func (OSFS) Copy(src, dst string) (err error) {
	in, err := os.Open(src) //nolint:gosec // the running tuios binary
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700) //nolint:gosec // a program, in a directory only this user can enter
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(dst)
		}
	}()
	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	return os.Chtimes(dst, st.ModTime(), st.ModTime())
}

// InstallFile makes link run exe, and reports how. Windows uses it, where a
// symbolic link needs a privilege a person rarely holds.
//
// A link that already runs exe is kept: the same file (a hardlink), a link
// whose target is exe, or a copy with exe's bytes. A copy is compared only
// when its size and modification time match exe's, since a clock that
// ticks coarsely can give two builds one time.
// Anything else is stale, for example a hardlink to the binary that
// tuios update replaced, or a copy of an older tuios, and is replaced.
//
// The new file is a hardlink when it can be, a copy when the link is on
// another volume, and a symbolic link only when both fail. It is made
// beside link and renamed onto it, so a reader never sees half a copy.
//
// Windows refuses to replace or delete a program that is running, and a
// plugin can be running the old link at this moment. It does allow a rename,
// so the old file is moved aside first and removed when it is no longer
// running, here or on a later start. tag keeps the names of two processes
// apart; the process id is enough.
func InstallFile(fsys FS, link, exe, tag string) (Method, error) {
	if m, ok := current(fsys, link, exe); ok {
		sweep(fsys, link)
		return m, nil
	}
	sweep(fsys, link)

	tmp := link + ".new-" + tag
	_ = fsys.Remove(tmp)
	var errs []error
	m := Method("")
	for _, try := range []struct {
		m  Method
		do func() error
	}{
		{MethodHardlink, func() error { return fsys.Link(exe, tmp) }},
		{MethodCopy, func() error { return fsys.Copy(exe, tmp) }},
		{MethodSymlink, func() error { return fsys.Symlink(exe, tmp) }},
	} {
		err := try.do()
		if err == nil {
			m = try.m
			break
		}
		errs = append(errs, fmt.Errorf("%s: %w", try.m, err))
		_ = fsys.Remove(tmp)
	}
	if m == "" {
		return "", fmt.Errorf("could not make %s: %w", link, errors.Join(errs...))
	}

	if err := fsys.Rename(tmp, link); err != nil {
		// The old file may be running. Move it aside, then try again.
		aside := link + ".old-" + tag
		_ = fsys.Remove(aside)
		if aerr := fsys.Rename(link, aside); aerr != nil {
			_ = fsys.Remove(tmp)
			return "", fmt.Errorf("could not replace %s: %w", link, errors.Join(err, aerr))
		}
		if err := fsys.Rename(tmp, link); err != nil {
			_ = fsys.Rename(aside, link)
			_ = fsys.Remove(tmp)
			return "", fmt.Errorf("could not replace %s: %w", link, err)
		}
		_ = fsys.Remove(aside)
	}
	return m, nil
}

// current reports whether link already runs exe, and how it does.
func current(fsys FS, link, exe string) (Method, bool) {
	lst, err := fsys.Lstat(link)
	if err != nil {
		return "", false
	}
	if lst.Mode()&fs.ModeSymlink != 0 {
		target, err := fsys.Readlink(link)
		return MethodSymlink, err == nil && target == exe
	}
	if !lst.Mode().IsRegular() {
		return "", false
	}
	est, err := fsys.Stat(exe)
	if err != nil {
		return "", false
	}
	if os.SameFile(lst, est) {
		return MethodHardlink, true
	}
	if lst.Size() == est.Size() && lst.ModTime().Equal(est.ModTime()) && sameBytes(fsys, link, exe) {
		return MethodCopy, true
	}
	return "", false
}

// sameBytes reports whether two files hold the same bytes. An error is a
// difference, so the link is made again.
func sameBytes(fsys FS, a, b string) bool {
	fa, err := fsys.Open(a)
	if err != nil {
		return false
	}
	defer func() { _ = fa.Close() }()
	fb, err := fsys.Open(b)
	if err != nil {
		return false
	}
	defer func() { _ = fb.Close() }()
	const chunk = 64 << 10
	ba, bb := make([]byte, chunk), make([]byte, chunk)
	for {
		na, ea := io.ReadFull(fa, ba)
		nb, eb := io.ReadFull(fb, bb)
		if na != nb || !bytes.Equal(ba[:na], bb[:nb]) {
			return false
		}
		doneA := errors.Is(ea, io.EOF) || errors.Is(ea, io.ErrUnexpectedEOF)
		doneB := errors.Is(eb, io.EOF) || errors.Is(eb, io.ErrUnexpectedEOF)
		switch {
		case doneA && doneB:
			return true
		case doneA != doneB, ea != nil && !doneA, eb != nil && !doneB:
			return false
		}
	}
}

// sweep removes the files an earlier InstallFile left beside link: a file
// moved aside while it ran, or a half-made one from a process that died.
// A file moved aside that still runs cannot be removed, and stays until a
// later start. A half-made file whose process still runs is that process's
// work in progress, and is left alone.
func sweep(fsys FS, link string) {
	for _, suffix := range []string{".old-", ".new-"} {
		names, err := fsys.Glob(globEscape(link) + suffix + "*")
		if err != nil {
			continue
		}
		for _, n := range names {
			if suffix == ".new-" {
				pid, err := strconv.Atoi(n[len(link)+len(suffix):])
				if err == nil && pid > 0 && fsys.Alive(pid) {
					continue
				}
			}
			_ = fsys.Remove(n)
		}
	}
}

// globEscape quotes the glob metacharacters in a path. On Windows a
// backslash is a separator and filepath.Glob has no escape, so there the
// characters are put in a class instead.
func globEscape(p string) string {
	var b strings.Builder
	for _, r := range p {
		switch r {
		case '*', '?', '[':
			b.WriteByte('[')
			b.WriteRune(r)
			b.WriteByte(']')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
