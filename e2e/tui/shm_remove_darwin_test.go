//go:build darwin

package tuie2e

import (
	"syscall"
	"unsafe"
)

// removeShmObject deletes a shared memory object a host stand-in never read. On
// macOS an object has no path.
func removeShmObject(name string) error {
	p, err := syscall.BytePtrFromString("/" + name)
	if err != nil {
		return err
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_SHM_UNLINK, uintptr(unsafe.Pointer(p)), 0, 0); errno != 0 {
		return errno
	}
	return nil
}
