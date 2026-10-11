//go:build windows

package session

import (
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// fdPath is the path the system has for the open file, with every link
// resolved, as it is now: a rename after the open shows here. ok is false
// when the system cannot say, or names it in a form the deny lists cannot
// judge.
func fdPath(f *os.File) (string, bool) {
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetFinalPathNameByHandle(windows.Handle(f.Fd()), &buf[0], uint32(len(buf)), 0)
	if err != nil || n == 0 || int(n) >= len(buf) {
		return "", false
	}
	p := windows.UTF16ToString(buf[:n])
	p = strings.TrimPrefix(p, `\\?\`)
	if !plainLinkPath(p) {
		return "", false
	}
	return p, true
}
