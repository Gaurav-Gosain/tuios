// Package atomicfile replaces a file in one step, so a crash leaves the old
// file or the new one whole and never half of one.
package atomicfile

import (
	"io/fs"
	"os"
	"path/filepath"
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
