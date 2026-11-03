//go:build !linux

package session

import (
	"os"
	"os/exec"
)

// execByDescriptor is false: with no /proc/self/fd, the preflight and the
// exec run a private copy of the checked binary (handoff_copy.go).
const execByDescriptor = false

// openBinary is not used here: the copy route opens with openForCopy.
func openBinary(string) (*os.File, error) { return nil, nil }

// runOpenFile leaves cmd running the path: only the copy route opens here.
func (handoffBinary) runOpenFile(*exec.Cmd) {}

// execPath is the path, for a binary that runs by it.
func (b handoffBinary) execPath() string { return b.path }

// isScript is false: the copy route finds scripts itself.
func (handoffBinary) isScript() bool { return false }

// setProcessName does nothing: an exec by path names the process after the
// file's base name, and the copy is named tuios.
func setProcessName(string) {}
