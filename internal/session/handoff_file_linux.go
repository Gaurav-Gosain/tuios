package session

import (
	"os"

	"golang.org/x/sys/unix"
)

func newHandoffFile(string) (*os.File, error) {
	fd, err := unix.MemfdCreate("tuios-handoff", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "tuios-handoff"), nil
}
