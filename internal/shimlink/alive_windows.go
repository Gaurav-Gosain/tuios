//go:build windows

package shimlink

import (
	"errors"

	"golang.org/x/sys/windows"
)

const stillActive = 259

// processAlive reports whether a process with this id runs.
func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)) //nolint:gosec // a process id fits
	if err != nil {
		// A process of another user cannot be opened, and it runs.
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
