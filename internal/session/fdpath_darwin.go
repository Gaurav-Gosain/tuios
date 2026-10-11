//go:build darwin

package session

import (
	"bytes"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// fdPath is the path the system has for the open file, with every link
// resolved, as it is now: a rename after the open shows here. ok is false
// when the system cannot say.
func fdPath(f *os.File) (string, bool) {
	buf := make([]byte, unix.PathMax)
	_, err := unix.FcntlInt(f.Fd(), unix.F_GETPATH, int(uintptr(unsafe.Pointer(&buf[0]))))
	runtime.KeepAlive(buf)
	if err != nil {
		return "", false
	}
	if i := bytes.IndexByte(buf, 0); i >= 0 {
		buf = buf[:i]
	}
	if len(buf) == 0 || buf[0] != '/' {
		return "", false
	}
	return string(buf), true
}
