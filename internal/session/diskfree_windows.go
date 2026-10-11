//go:build windows

package session

import "golang.org/x/sys/windows"

// diskFree is the bytes the user may still use on the disk that holds dir,
// or -1 when it cannot be read.
func diskFree(dir string) int64 {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return -1
	}
	var free, total, all uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &all); err != nil {
		return -1
	}
	return int64(free) //nolint:gosec // a disk is far below int64's range
}
