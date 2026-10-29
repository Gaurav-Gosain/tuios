//go:build !linux && !darwin && !windows && !js

package session

import "syscall"

// changeTime is not read here: the stat field's name differs by system, and
// the size and modification time still pin the file.
func changeTime(*syscall.Stat_t) int64 { return 0 }
