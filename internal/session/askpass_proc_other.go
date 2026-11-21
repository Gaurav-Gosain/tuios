//go:build !linux

package session

import "github.com/Gaurav-Gosain/tuios/internal/procinfo"

// canWaitForeignExit says waitForeignExit blocks until the process ends,
// which it does on macOS and not on the platforms with no way to wait.
const canWaitForeignExit = canPeekExit

// askpassParent is the parent of pid.
func askpassParent(pid int) (int, bool) { return procinfo.ParentPID(pid) }

// askingProgram cannot read another process's command line here: the dialog
// shows the program as unknown.
func askingProgram(int) (exe, line string) { return "", "" }
