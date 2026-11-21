package session

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func cloneFrom(src *os.File, dst string) error {
	dir, err := os.Open(filepath.Dir(dst))
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	if err := unix.Fclonefileat(int(src.Fd()), int(dir.Fd()), filepath.Base(dst), unix.CLONE_NOOWNERCOPY); err != nil { //nolint:gosec // descriptor numbers
		return err
	}
	if err := os.Chmod(dst, 0o700); err != nil { //nolint:gosec // the clone is the tuios binary the handoff runs: it needs its owner's execute bit, and no one else gets any
		_ = os.Remove(dst)
		return err
	}
	return nil
}

const quarantineAttr = "com.apple.quarantine"

func copyQuarantine(src, dst *os.File) error {
	buf := make([]byte, 1024)
	n, err := unix.Fgetxattr(fdNum(src), quarantineAttr, buf)
	if errors.Is(err, unix.ENOATTR) {
		return nil
	}
	if err != nil {
		return err
	}
	return unix.Fsetxattr(fdNum(dst), quarantineAttr, buf[:n], 0)
}
