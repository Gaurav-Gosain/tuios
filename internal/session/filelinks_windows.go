//go:build windows

package session

import (
	"os"

	"golang.org/x/sys/windows"
)

// fileLinks is the number of names the open file has: more than one means a
// hard link elsewhere shares its bytes.
func fileLinks(f *os.File) (uint64, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return 0, err
	}
	return uint64(info.NumberOfLinks), nil
}
