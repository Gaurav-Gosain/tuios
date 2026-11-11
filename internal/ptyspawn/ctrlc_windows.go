//go:build windows

package ptyspawn

import "golang.org/x/sys/windows"

var procSetConsoleCtrlHandler = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleCtrlHandler")

// AllowCtrlC turns Ctrl+C handling back on for this process, so the programs
// it starts in panes get Ctrl+C again.
//
// A process started with CREATE_NEW_PROCESS_GROUP, as the daemon is, starts
// with Ctrl+C handling off, and every process it starts inherits that. A pane's
// program then ignores the Ctrl+C its pseudo console sends when the person
// presses Ctrl+C in the pane (issue #1668). SetConsoleCtrlHandler(NULL, FALSE)
// turns it on again, for this process and for the processes it starts after.
// The daemon's own console has no window, so nothing types Ctrl+C into it.
func AllowCtrlC() error {
	if r, _, err := procSetConsoleCtrlHandler.Call(0, 0); r == 0 {
		return err
	}
	return nil
}
