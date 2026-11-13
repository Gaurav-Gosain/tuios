//go:build unix

package main

import (
	"os"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"golang.org/x/sys/unix"
)

// ownTerminal opens this server's own handle on a session's terminal: a
// second descriptor for the same PTY slave, closed on exec so no pane program
// inherits it. See createTUIOSProgram for why the session needs one.
func ownTerminal(slave *os.File) (*os.File, error) {
	raw, err := slave.SyscallConn()
	if err != nil {
		return nil, errfmt.Errorf("terminal descriptor: %w", err)
	}
	fd, dupErr := -1, error(nil)
	if err := raw.Control(func(s uintptr) { fd, dupErr = unix.FcntlInt(s, unix.F_DUPFD_CLOEXEC, 0) }); err != nil {
		return nil, errfmt.Errorf("terminal descriptor: %w", err)
	}
	if dupErr != nil {
		return nil, errfmt.Errorf("duplicate terminal: %w", dupErr)
	}
	return os.NewFile(uintptr(fd), slave.Name()), nil
}
