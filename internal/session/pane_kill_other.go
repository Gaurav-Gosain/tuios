//go:build !unix || js

package session

import "os"

// killPaneTree kills the pane's process. On Windows a pane's processes are
// not in a job object: closing the pseudo console, which Close does next,
// ends the processes still attached to the pane's console, and a process
// that left it (one started with a console or window of its own) runs on.
func killPaneTree(proc *os.Process) {
	if proc != nil {
		_ = proc.Kill()
	}
}
