//go:build windows || js

package ptyspawn

// HangUp does nothing here and reports false. On Windows a pane's processes
// are not in a job object: closing the pseudo console ends the processes
// still attached to it.
func HangUp(int) bool { return false }
