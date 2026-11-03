//go:build linux

package session

import (
	"io"
	"os"
	"os/exec"
	"strconv"

	"golang.org/x/sys/unix"
)

// execByDescriptor says the checked binary runs through its open file here,
// not through a private copy.
const execByDescriptor = true

// openBinary opens the binary at abs for the checks, the preflight and the
// exec, which then run the open file, so no rename over the path after the
// open can change what runs. O_PATH needs no read permission, so an
// execute-only binary opens as it runs today. The descriptor is
// close-on-exec: the new image and its programs never hold it.
func openBinary(abs string) (*os.File, error) {
	fd, err := unix.Open(abs, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: abs, Err: err}
	}
	return os.NewFile(uintptr(fd), abs), nil
}

// runOpenFile points cmd at the open file, handed to it as descriptor 3.
// Its argv[0] stays the path.
func (b handoffBinary) runOpenFile(cmd *exec.Cmd) {
	if b.open != nil {
		cmd.Path, cmd.ExtraFiles = "/proc/self/fd/3", []*os.File{b.open}
	}
}

// execPath is what the exec runs: the open file.
func (b handoffBinary) execPath() string {
	if b.open == nil {
		return b.path
	}
	return "/proc/self/fd/" + strconv.Itoa(int(b.open.Fd())) //nolint:gosec // a descriptor number
}

// isScript reports whether the open file starts with #!. A script cannot run
// through a descriptor its interpreter must open again, so it runs by its
// path. A file that cannot be read is not a script.
func (b handoffBinary) isScript() bool {
	if b.open == nil {
		return false
	}
	f, err := os.Open(b.execPath())
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	var head [2]byte
	_, err = io.ReadFull(f, head[:])
	return err == nil && string(head[:]) == "#!"
}

// setProcessName names this process, as ps and pidof show it. An image run
// through /proc/self/fd/N is named N by the kernel. /proc/self/comm is the
// main thread's name, the one ps and pidof show, whichever thread writes it;
// the other threads keep N. An image started from a file it cannot read
// cannot write it, and keeps the number.
func setProcessName(name string) {
	if len(name) > 15 {
		name = name[:15]
	}
	f, err := os.OpenFile("/proc/self/comm", os.O_WRONLY, 0)
	if err == nil {
		_, err = f.WriteString(name)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		LogBasic("Live upgrade: the process keeps its name, it could not be set to %s: %v", name, err)
	}
}
