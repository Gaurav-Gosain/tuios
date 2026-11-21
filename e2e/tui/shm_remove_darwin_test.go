//go:build darwin

package tuie2e

import (
	"syscall"
	"unsafe"
)

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
