//go:build !windows

package session

import "syscall"

// makeFIFO creates a named pipe, so a test can hand the stash something that is
// not a regular file and would block forever if it were opened and read.
func makeFIFO(path string) error {
	return syscall.Mkfifo(path, 0o600)
}
