//go:build unix || linux || darwin || freebsd || openbsd || netbsd

package terminal

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ForegroundCommand returns the command name of the pane's foreground process
// group, or "" when it cannot be read.
//
// This is the one thing tuios can observe about what is running inside a pane
// rather than infer. The pane's program is asked nothing: the kernel is asked
// which process group owns the terminal, and /proc is asked what that group is
// called. It is read from /proc, so it is Linux (and some BSDs) and empty
// elsewhere, and callers must treat "" as "not known" rather than as "nothing
// is running".
//
// Uncached on purpose, unlike CWD. Nothing on the render path calls this: it is
// read when the keybind overlay opens and when a key is recorded, which is a
// handful of times per session rather than once per window per frame. A cache
// here would only buy a stale answer at the one moment the answer matters.
func (w *Window) ForegroundCommand() string {
	if w.Pty == nil {
		return ""
	}

	var fgpgrp int
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		w.Pty.Fd(),
		uintptr(unix.TIOCGPGRP),
		uintptr(unsafe.Pointer(&fgpgrp)),
	)
	if errno != 0 || fgpgrp <= 0 {
		return ""
	}

	comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", fgpgrp))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(comm))
}
