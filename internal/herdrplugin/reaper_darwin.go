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

// waitExited blocks until process pid exits, and leaves it unreaped, so its
// pid stays taken while the caller drops what it knew about it. A kqueue
// EVFILT_PROC NOTE_EXIT filter waits without reaping, as WNOWAIT does on
// Linux. ESRCH means the process already exited: it is a zombie until
// cmd.Wait, so its pid is still taken.
func waitExited(pid int) {
	kq, err := unix.Kqueue()
	if err != nil {
		waitZombie(pid)
		return
	}
	defer unix.Close(kq)
	change := []unix.Kevent_t{{}}
	unix.SetKevent(&change[0], pid, unix.EVFILT_PROC, unix.EV_ADD|unix.EV_ONESHOT)
	change[0].Fflags = unix.NOTE_EXIT
	events := make([]unix.Kevent_t, 1)
	for {
		n, err := unix.Kevent(kq, change, events, nil)
		switch {
		case err == unix.EINTR:
			continue
		case err == unix.ESRCH:
			return
		case err != nil:
			waitZombie(pid)
			return
		case n == 1 && events[0].Flags&unix.EV_ERROR != 0:
			if unix.Errno(events[0].Data) == unix.ESRCH {
				return
			}
			waitZombie(pid)
			return
		case n == 1:
			return
		}
	}
}

// waitZombie is the fallback when no kqueue can be had: it polls until pid
// is a zombie or gone. It never returns while the process runs, so the
// owner entry is never dropped early.
func waitZombie(pid int) {
	for {
		kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
		if err != nil || kp.Proc.P_pid != int32(pid) || kp.Proc.P_stat == sZomb {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// sZomb is SZOMB from <sys/proc.h>: the process exited and is not reaped.
const sZomb = 5
