//go:build !unix || js

package session

import "os"

func killPaneTree(proc *os.Process) {
	if proc != nil {
		_ = proc.Kill()
	}
}
