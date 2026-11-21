package integration

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// BackupSuffix is appended to a file's name for the copy kept before tuios
// first rewrites it.
const BackupSuffix = ".tuios.bak"

func readOptional(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

func resolveWriteTarget(path string) (string, error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return path, nil
	}
	if err != nil {
		return "", errfmt.Errorf("failed to read %s: %w", path, err)
	}
	if fi.Mode()&fs.ModeSymlink == 0 {
		return path, nil
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", errfmt.Errorf("%s is a symlink tuios cannot follow, so it was left unchanged: %w", path, err)
	}
	return real, nil
}

// ErrFileChanged is returned when a file changed between the read an edit was
// worked out from and the write that would replace it. Nothing is written over
// the other program's change.
var ErrFileChanged = errors.New("the file changed while tuios was editing it, so tuios did not write it. Try again")

func sameAsRead(target string, have []byte) (bool, error) {
	now, err := readOptional(target)
	if err != nil {
		return false, err
	}
	return (now == nil) == (have == nil) && bytes.Equal(now, have), nil
}

func retryChanged(op func() (Result, error)) (Result, error) {
	res, err := op()
	if errors.Is(err, ErrFileChanged) {
		first := res
		res, err = op()
		for _, p := range first.Paths {
			if !slices.Contains(res.Paths, p) {
				res.Paths = append([]string{p}, res.Paths...)
			}
		}
		res.Changed = res.Changed || first.Changed
		if res.Backup == "" {
			res.Backup = first.Backup
		}
	}
	if err != nil && len(res.Paths) > 0 {
		err = errfmt.Errorf("%w. tuios already changed %s. It did not change %s",
			err, strings.Join(res.Paths, ", "), strings.Join(res.notWritten, ", "))
	}
	return res, err
}

var beforeRename func(target string)

func writeAtomic(path string, have, data []byte) error {
	target, err := resolveWriteTarget(path)
	if err != nil {
		return err
	}
	if same, err := sameAsRead(target, have); err != nil {
		return errfmt.Errorf("failed to read %s: %w", path, err)
	} else if !same {
		return errfmt.Errorf("%s: %w", path, ErrFileChanged)
	}
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // another tool's config directory, made the way that tool makes it
		return errfmt.Errorf("failed to create %s: %w", dir, err)
	}
	mode := fs.FileMode(0o644)
	if old, err := os.ReadFile(target); err == nil {
		if st, err := os.Stat(target); err == nil {
			mode = st.Mode().Perm()
		}
		backup := path + BackupSuffix
		if _, err := os.Lstat(backup); errors.Is(err, fs.ErrNotExist) {
			if err := os.WriteFile(backup, old, mode); err != nil {
				return errfmt.Errorf("failed to back up %s: %w", path, err)
			}
		} else if err != nil {
			return errfmt.Errorf("failed to back up %s: %w", path, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return errfmt.Errorf("failed to read %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(target)+".tuios-*")
	if err != nil {
		return errfmt.Errorf("failed to write %s: %w", path, err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return errfmt.Errorf("failed to write %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return errfmt.Errorf("failed to write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return errfmt.Errorf("failed to write %s: %w", path, err)
	}
	if err := os.Chmod(name, mode); err != nil {
		return errfmt.Errorf("failed to write %s: %w", path, err)
	}
	if beforeRename != nil {
		beforeRename(target)
	}
	if same, err := sameAsRead(target, have); err != nil {
		return errfmt.Errorf("failed to read %s: %w", path, err)
	} else if !same {
		return errfmt.Errorf("%s: %w", path, ErrFileChanged)
	}
	if err := os.Rename(name, target); err != nil {
		return errfmt.Errorf("failed to replace %s: %w", path, err)
	}
	return nil
}
