package session

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// cloneFrom makes dst an APFS clone of the open file src, owned by this
// user and readable only by it. It fails on a file system without clones,
// or across volumes.
func cloneFrom(src *os.File, dst string) error {
	dir, err := os.Open(filepath.Dir(dst))
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	if err := unix.Fclonefileat(int(src.Fd()), int(dir.Fd()), filepath.Base(dst), unix.CLONE_NOOWNERCOPY); err != nil { //nolint:gosec // descriptor numbers
		return err
	}
	if err := os.Chmod(dst, 0o700); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return nil
}

// quarantineAttr marks a file a browser or another app downloaded.
// Gatekeeper checks such a file before its first run.
const quarantineAttr = "com.apple.quarantine"

// copyQuarantine gives dst the quarantine mark src has, so Gatekeeper judges
// the private copy as it judges the binary the person named. Without it a
// downloaded binary would run through the copy with no check at all.
func copyQuarantine(src, dst *os.File) error {
	buf := make([]byte, 1024)
	n, err := unix.Fgetxattr(int(src.Fd()), quarantineAttr, buf) //nolint:gosec // a descriptor number
	if errors.Is(err, unix.ENOATTR) {
		return nil
	}
	if err != nil {
		return err
	}
	return unix.Fsetxattr(int(dst.Fd()), quarantineAttr, buf[:n], 0) //nolint:gosec // a descriptor number
}
