//go:build !linux

package session

import (
	"os"
	"os/exec"
)

// openBinary opens nothing: with no /proc/self/fd, the checks, the preflight
// and the exec use the path.
func openBinary(string) (*os.File, error) { return nil, nil }

// runOpenFile leaves cmd running the path.
func (handoffBinary) runOpenFile(*exec.Cmd) {}

// execPath is what the exec runs: the path.
func (b handoffBinary) execPath() string { return b.path }

// isScript is false: everything runs by its path here.
func (handoffBinary) isScript() bool { return false }

// setProcessName does nothing: an exec by path keeps the process's name.
func setProcessName(string) {}
