//go:build !linux && !darwin && !windows && !js

package session

import "syscall"

func changeTime(*syscall.Stat_t) int64 { return 0 }
