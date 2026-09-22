package integration

import (
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
func writeAtomic(path string, data []byte) error {
	target, err := resolveWriteTarget(path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
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
	if err := os.Rename(name, target); err != nil {
		return fmt.Errorf("failed to replace %s: %w", path, err)
	}
	return nil
}
