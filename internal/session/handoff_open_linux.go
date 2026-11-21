//go:build linux

package session

import (
	"io"
	"os"
	"os/exec"
	"strconv"

	"golang.org/x/sys/unix"
)

const execByDescriptor = true

func openBinary(abs string) (*os.File, error) {
	fd, err := unix.Open(abs, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: abs, Err: err}
	}
	return os.NewFile(uintptr(fd), abs), nil
}

func (b handoffBinary) runOpenFile(cmd *exec.Cmd) {
	if b.open != nil {
		cmd.Path, cmd.ExtraFiles = "/proc/self/fd/3", []*os.File{b.open}
	}
}

func (b handoffBinary) execPath() string {
	if b.open == nil {
		return b.path
	}
	return "/proc/self/fd/" + strconv.Itoa(fdNum(b.open))
}

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
