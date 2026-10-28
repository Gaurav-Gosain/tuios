//go:build unix

package session

import "syscall"

// processUmask is the umask this process started with. The umask is
// process-wide and can only be read by setting it, so it is read once here,
// before any goroutine of this package creates a file.
var processUmask = func() uint32 {
	old := syscall.Umask(0o077)
	syscall.Umask(old)
	return uint32(old) //nolint:gosec // a umask is 0 to 0o777
}()
