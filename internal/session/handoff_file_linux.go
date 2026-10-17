package session

import (
	"os"

	"golang.org/x/sys/unix"
)

// newHandoffFile makes the descriptor the manifest is written to: a memfd, so
// the manifest, which holds the pane-token key and every pane's screen, never
// touches a disk. The exec keeps it, and its offset with it.
func newHandoffFile(string) (*os.File, error) {
	fd, err := unix.MemfdCreate("tuios-handoff", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "tuios-handoff"), nil
}
