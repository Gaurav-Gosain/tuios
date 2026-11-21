//go:build unix

package session

import "syscall"

var processUmask = func() uint32 {
	old := syscall.Umask(0o077)
	syscall.Umask(old)
	return uint32(old) //nolint:gosec // a umask is 0 to 0o777
}()
