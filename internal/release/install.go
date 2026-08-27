package release

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Replacing a binary that is running.
//
// A daemon is very likely executing the exact file being replaced, and on Linux
// writing to a running executable fails outright with ETXTBSY. Renaming over it
// does not: the running process keeps the inode it already opened and the name
// points at the new file, which is what every installer in this repository
// already does. The rename has to happen inside the target's own directory,
// because a rename across filesystems is not a rename at all and os.Rename
// fails with EXDEV.
//
// Staged is the pair of steps that arrangement needs: write the bytes next to
// the target, then move them onto it.

// Staged is a new binary written beside the file it will replace, not yet in
// place.
type Staged struct {
	// Target is where it is going.
	Target string
	// temp is the staged file, in Target's own directory.
	temp string
}

// Stage writes data next to target, ready to be moved onto it.
//
// The temporary name carries the process id so two updates running at once
// cannot write to the same staging file, and it starts with a dot so a
// half-finished update in a directory on PATH is not something a shell offers
// to complete.
func Stage(target string, data []byte, mode os.FileMode) (*Staged, error) {
	dir := filepath.Dir(target)
	f, err := os.CreateTemp(dir, "."+filepath.Base(target)+".update-*")
	if err != nil {
		return nil, fmt.Errorf("failed to write next to %s: %w", target, err)
	}
	name := f.Name()
	cleanup := func() { _ = f.Close(); _ = os.Remove(name) }

	if _, err := f.Write(data); err != nil {
		cleanup()
		return nil, fmt.Errorf("failed to write %s: %w", name, err)
	}
	// Flushed before the rename, so a machine that loses power between the two
	// is left with either the old binary or a complete new one, never a
	// truncated file under the real name.
	if err := f.Sync(); err != nil {
		cleanup()
		return nil, fmt.Errorf("failed to flush %s: %w", name, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return nil, fmt.Errorf("failed to close %s: %w", name, err)
	}
	// CreateTemp makes the file 0600. The mode is set explicitly rather than
	// left to it, or the replacement would be a binary only its owner can run.
	if err := os.Chmod(name, mode); err != nil {
		_ = os.Remove(name)
		return nil, fmt.Errorf("failed to set the mode on %s: %w", name, err)
	}
	return &Staged{Target: target, temp: name}, nil
}

// Commit moves the staged file onto its target.
func (s *Staged) Commit() error {
	if s == nil || s.temp == "" {
		return errors.New("nothing staged")
	}
	if err := os.Rename(s.temp, s.Target); err != nil {
		_ = os.Remove(s.temp)
		s.temp = ""
		return fmt.Errorf("failed to move the new binary onto %s: %w", s.Target, err)
	}
	s.temp = ""
	return nil
}

// Discard removes the staged file. Safe to call after Commit, and safe to
// defer.
func (s *Staged) Discard() {
	if s == nil || s.temp == "" {
		return
	}
	_ = os.Remove(s.temp)
	s.temp = ""
}

// BinaryMode is the mode a replacement should get: the mode the file being
// replaced already has, or 0755 when there is nothing there to ask.
//
// Copying the existing mode matters for the curl script's own installs. It
// chmods to 0755 in a user directory and leaves whatever /usr/local/bin was set
// to elsewhere, and an update that imposed its own mode would silently change
// how the binary is shared on a multi-user machine.
func BinaryMode(target string) os.FileMode {
	if info, err := os.Stat(target); err == nil {
		if mode := info.Mode().Perm(); mode != 0 {
			return mode
		}
	}
	return 0o755
}

// Writable reports whether a new file can be created in dir, which is what the
// rename actually needs. It is not the same question as whether the target file
// is writable: a read-only file in a writable directory can be replaced, and a
// writable file in a directory owned by root cannot.
//
// Tested by trying rather than by reading permission bits, because the bits do
// not account for the effective user, for group membership, or for a read-only
// mount, and the answer has to be right or the command refuses for the wrong
// reason.
func Writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".tuios-update-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// ExecutableName is the file name a binary has on this platform.
func ExecutableName(binary string) string {
	if runtime.GOOS == "windows" {
		return binary + ".exe"
	}
	return binary
}
