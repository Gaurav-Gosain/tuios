//go:build !linux

package session

import "github.com/Gaurav-Gosain/tuios/internal/procinfo"

// askpassParent is the parent of pid.
func askpassParent(pid int) (int, bool) { return procinfo.ParentPID(pid) }

// askingProgram cannot read another process's command line here: the dialog
// says it cannot tell which program asks.
func askingProgram(int) []string { return nil }
