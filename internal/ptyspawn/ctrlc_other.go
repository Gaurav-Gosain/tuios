//go:build !windows

package ptyspawn

// AllowCtrlC does nothing outside Windows, where a process group does not
// decide whether a pane's program gets Ctrl+C. See ctrlc_windows.go.
func AllowCtrlC() error { return nil }
