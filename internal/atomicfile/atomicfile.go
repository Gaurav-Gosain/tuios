// Package atomicfile replaces a file in one step, so a crash leaves the old
// file or the new one whole and never half of one.
package atomicfile

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Write replaces path with data. It writes a temporary file with a fresh name
// in path's folder, gives it mode perm, and renames it over path. The folder
// must exist.
//
// The fresh name comes from os.CreateTemp, so a leftover or planted file at a
// fixed name, a symlink included, is never written through, two writers never
// share one temporary file, and the old file's mode never carries over. The
// temporary file is named "."+base(path)+".*.tmp", so a folder listing can
// tell it apart, and it is removed when the write fails.
//
// With sync, the file is fsynced before the rename and the folder after it,
// so the new file also survives a power loss right after Write returns. That
// costs a disk flush, so a caller that writes often and can lose the last
// write passes false.
func Write(path string, data []byte, perm fs.FileMode, sync bool) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	err = f.Chmod(perm)
	if err == nil {
		_, err = f.Write(data)
	}
	if err == nil && sync {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if sync {
		if df, err := os.Open(dir); err == nil {
			_ = df.Sync()
			_ = df.Close()
		}
	}
	return nil
}

// WritePrivate is Write for a file only its owner may read: the folder is made
// with mode 0700 when it is missing, and the file gets mode 0600. A folder
// that already exists keeps its mode.
func WritePrivate(path string, data []byte, sync bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return Write(path, data, 0o600, sync)
}

// SweepStale removes, from dir, the temporary files Write leaves behind when it
// dies between creating one and renaming it: regular files named ".<base>.*.tmp"
// that were last written more than olderThan ago. The age keeps it off a write
// another process has in flight. It is best effort and reports nothing.
func SweepStale(dir string, olderThan time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-olderThan)
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || !isTempName(name) {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

// isTempName reports whether name has the shape Write gives a temporary file:
// a dot, the base name, a dot, the random part, and ".tmp".
func isTempName(name string) bool {
	rest, ok := strings.CutPrefix(name, ".")
	if !ok {
		return false
	}
	rest, ok = strings.CutSuffix(rest, ".tmp")
	return ok && strings.Contains(rest, ".")
}
