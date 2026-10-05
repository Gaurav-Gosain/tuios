package integration

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// BackupSuffix is appended to a file's name for the copy kept before tuios
// first rewrites it.
const BackupSuffix = ".tuios.bak"

// readOptional reads a file, returning nil and no error when it does not exist.
func readOptional(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

// resolveWriteTarget returns the file a write to path should replace. A
// symlink is followed to the file it names, so a settings file a dotfile
// manager (stow, home-manager, a bare repo) links into place stays a link and
// the change lands in the linked file. A path that does not exist yet is
// written where it is. A link whose target does not exist is refused: writing
// through it would create a file somewhere the user may not expect, and
// replacing it would drop the link.
func resolveWriteTarget(path string) (string, error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return path, nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", path, err)
	}
	if fi.Mode()&fs.ModeSymlink == 0 {
		return path, nil
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("%s is a symlink tuios cannot follow, so it was left unchanged: %w", path, err)
	}
	return real, nil
}

// ErrFileChanged is returned when a file changed between the read an edit was
// worked out from and the write that would replace it. Nothing is written over
// the other program's change.
var ErrFileChanged = errors.New("the file changed while tuios was editing it, so it was left as it is. Try again")

// sameAsRead reports whether the file at target still holds have, the bytes
// the edit was worked out from. A nil have means the file did not exist.
func sameAsRead(target string, have []byte) (bool, error) {
	now, err := readOptional(target)
	if err != nil {
		return false, err
	}
	return (now == nil) == (have == nil) && bytes.Equal(now, have), nil
}

// retryChanged runs op, and once more when the file changed under it, so an
// edit worked out from a file another program was saving is worked out again
// from the saved file. A second change in a row is reported.
func retryChanged(op func() (Result, error)) (Result, error) {
	res, err := op()
	if errors.Is(err, ErrFileChanged) {
		res, err = op()
	}
	return res, err
}

// beforeRename, when set, runs just before writeAtomic checks the file a last
// time. The tests use it to change the file at the worst moment.
var beforeRename func(target string)

// writeAtomic replaces path with data so a reader, the harness included, sees
// either the old file or the new one and never half of each: the data goes to
// a temporary file in the same directory, is synced, and is renamed over the
// target. When path is a symlink the file it points to is the one replaced,
// so the link itself is kept.
//
// The first time tuios rewrites a file, the file as it was is copied to
// path+BackupSuffix. A later write keeps that copy rather than overwriting
// it, so the backup is always the file from before tuios touched it. Its
// permissions are kept.
//
// have is the file as it was read when the edit was worked out, nil when it
// did not exist. The file is read again just before the rename, and a file
// that no longer holds have is left alone with ErrFileChanged: the harness or
// the person saved it in between, and renaming over it would drop that save.
func writeAtomic(path string, have, data []byte) error {
	target, err := resolveWriteTarget(path)
	if err != nil {
		return err
	}
	if same, err := sameAsRead(target, have); err != nil {
		return fmt.Errorf("failed to read %s: %w", path, err)
	} else if !same {
		return fmt.Errorf("%s: %w", path, ErrFileChanged)
	}
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // another tool's config directory, made the way that tool makes it
		return fmt.Errorf("failed to create %s: %w", dir, err)
	}
	mode := fs.FileMode(0o644)
	if old, err := os.ReadFile(target); err == nil {
		if st, err := os.Stat(target); err == nil {
			mode = st.Mode().Perm()
		}
		backup := path + BackupSuffix
		if _, err := os.Lstat(backup); errors.Is(err, fs.ErrNotExist) {
			if err := os.WriteFile(backup, old, mode); err != nil {
				return fmt.Errorf("failed to back up %s: %w", path, err)
			}
		} else if err != nil {
			return fmt.Errorf("failed to back up %s: %w", path, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("failed to read %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(target)+".tuios-*")
	if err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	if err := os.Chmod(name, mode); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	if beforeRename != nil {
		beforeRename(target)
	}
	if same, err := sameAsRead(target, have); err != nil {
		return fmt.Errorf("failed to read %s: %w", path, err)
	} else if !same {
		return fmt.Errorf("%s: %w", path, ErrFileChanged)
	}
	if err := os.Rename(name, target); err != nil {
		return fmt.Errorf("failed to replace %s: %w", path, err)
	}
	return nil
}
