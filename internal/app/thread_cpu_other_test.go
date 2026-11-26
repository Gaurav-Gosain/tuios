//go:build !linux && !darwin

package app

import "time"

// threadCPU has no per-thread clock to read here.
func threadCPU() (time.Duration, bool) { return 0, false }
