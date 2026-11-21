//go:build !windows && !js

package session

import (
	"net"
	"os"
	"syscall"

	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
	"golang.org/x/sys/unix"
)

func handoffSupported() bool { return canPeekExit }

func listenerFD(l net.Listener, fn func(fd int)) bool {
	sc, ok := l.(syscall.Conn)
	if !ok {
		return false
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return false
	}
	return raw.Control(func(fd uintptr) { fn(int(fd)) }) == nil
}

func execHandoff(path string, argv, env []string, fds []int, inWindow func()) error {
	syscall.ForkLock.Lock()
	defer syscall.ForkLock.Unlock()
	cleared := make([]int, 0, len(fds))
	defer func() {
		for _, fd := range cleared {
			unix.CloseOnExec(fd)
		}
	}()
	for _, fd := range fds {
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, 0); err != nil { //nolint:gosec // a descriptor number
			return err
		}
		cleared = append(cleared, fd)
	}
	if inWindow != nil {
		inWindow()
	}
	return syscall.Exec(path, argv, env)
}

func adoptedFile(fd int, name string) *os.File {
	unix.CloseOnExec(fd)
	return os.NewFile(uintptr(fd), name) //nolint:gosec // a descriptor number
}

func adoptMaster(fd int, tty string) (paneIO, error) {
	return ptyspawn.Adopt(fd, tty)
}

func rewindDescriptor(fd int) error {
	_, err := unix.Seek(fd, 0, 0)
	return err
}
