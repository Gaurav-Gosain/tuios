package session

import (
	"errors"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

// askpassParent is the parent of pid.
func askpassParent(pid int) (int, bool) { return procinfo.ParentPID(pid) }

// askingProgram reads the resolved executable of pid and its arguments from
// the kernel. Either is empty when it cannot be read.
func askingProgram(pid int) (exe string, args []string) {
	dir := "/proc/" + strconv.Itoa(pid)
	exe, _ = os.Readlink(dir + "/exe")
	raw, err := os.ReadFile(dir + "/cmdline")
	if err != nil || len(raw) == 0 {
		return exe, nil
	}
	return exe, strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
}

// pidfdOpen is unix.PidfdOpen, which a test replaces to stand for a kernel
// or a seccomp profile that refuses it.
var pidfdOpen = unix.PidfdOpen

// askpassWatch returns a channel that closes when process pid exits. A pidfd
// tells at once. Where the kernel gives none (before Linux 5.3, or under a
// seccomp profile that does not know the call), the process is looked at
// every askpassPoll until stop closes: only while a prompt is open.
func askpassWatch(pid int, stop <-chan struct{}) <-chan struct{} {
	gone := make(chan struct{})
	if pid <= 0 {
		return gone
	}
	fd, err := pidfdOpen(pid, 0)
	if err != nil {
		go pollExit(pid, stop, gone)
		return gone
	}
	go func() {
		defer func() { _ = unix.Close(fd) }()
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}} //nolint:gosec // a descriptor number
		for {
			if _, err := unix.Poll(fds, -1); !errors.Is(err, unix.EINTR) {
				close(gone)
				return
			}
		}
	}()
	return gone
}
