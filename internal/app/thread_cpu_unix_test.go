//go:build linux || darwin

package app

import (
	"time"

	"golang.org/x/sys/unix"
)

// threadCPU is the CPU time the calling OS thread has used. It counts only
// the time the thread ran, so time the scheduler gave to other processes is
// not in it. The caller locks its goroutine to the thread between two reads.
func threadCPU() (time.Duration, bool) {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_THREAD_CPUTIME_ID, &ts); err != nil {
		return 0, false
	}
	return time.Duration(ts.Nano()), true
}
