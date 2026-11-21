//go:build darwin

package herdrplugin

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

const waitsForGroup = false

func becomeSubreaper() {}

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

func waitZombie(pid int) {
	for {
		kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
		if err != nil || kp.Proc.P_pid != int32(pid) || kp.Proc.P_stat == sZomb {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

const sZomb = 5
