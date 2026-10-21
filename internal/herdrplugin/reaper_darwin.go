//go:build darwin

package herdrplugin

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// waitsForGroup: without a subreaper, what the command left is not the
// reaper's child, and waitForGroup looks at the process group instead.
const waitsForGroup = false

func becomeSubreaper() {}

// waitForGroup returns when the reaper is the last process in its group. It
// looks once a second, and only runs while something the command left still
// runs.
func waitForGroup() {
	pgid := os.Getpid()
	for {
		procs, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", pgid)
		if err != nil || len(procs) <= 1 {
			return
		}
		time.Sleep(time.Second)
	}
}

func waitExited(int) {}
