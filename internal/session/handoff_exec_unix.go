//go:build !windows && !js

package session

import (
	"net"
	"os"
	"syscall"

	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
	"golang.org/x/sys/unix"
)

// handoffSupported reports whether this build can hand its panes to another
// image: it needs to wait for a pane's exit without reaping it.
func handoffSupported() bool { return canPeekExit }

// listenerFD runs fn with l's descriptor, and reports false for a listener
// that has none.
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

// execHandoff replaces this process's image with path, keeping fds open
// across the exec. It returns only when the exec failed, with every
// descriptor back to close-on-exec.
//
// The descriptors are exposed for the shortest span there is, and under
// syscall.ForkLock held for writing: a fork in that span would hand a PTY
// master to a child that outlives this image, so the pane never hangs up,
// and the start lock to one that holds it after the daemon is gone, so no
// daemon can start again. Every fork in the process takes ForkLock, so none
// can start until the exec has replaced the process or the descriptors are
// closed-on-exec again. inWindow, when not nil, runs inside that span; only a
// test sets it.
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

// adoptedFile wraps a descriptor inherited across the exec, closed on the
// next one like every descriptor Go opens.
func adoptedFile(fd int, name string) *os.File {
	unix.CloseOnExec(fd)
	return os.NewFile(uintptr(fd), name) //nolint:gosec // a descriptor number
}

// adoptMaster wraps an inherited PTY master.
func adoptMaster(fd int, tty string) (paneIO, error) {
	return ptyspawn.Adopt(fd, tty)
}

// rewindDescriptor seeks fd to its start. The manifest's offset is shared
// across the exec, and the image that reads it next starts at the top.
func rewindDescriptor(fd int) error {
	_, err := unix.Seek(fd, 0, 0)
	return err
}
