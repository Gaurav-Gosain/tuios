//go:build darwin

package procinfo

import (
	"net"
	"time"

	"golang.org/x/sys/unix"
)

// PeerPID returns the pid of the process on the other end of a unix socket,
// as the kernel recorded it at connect time (LOCAL_PEERPID), or 0 when it does
// not say.
func PeerPID(conn net.Conn) int {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return 0
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0
	}
	pid := 0
	_ = raw.Control(func(fd uintptr) {
		if v, err := unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID); err == nil {
			pid = v
		}
	})
	return pid
}

// StartTime returns when process pid started, in microseconds since the
// epoch, and false when it cannot be read.
func StartTime(pid int) (uint64, bool) {
	if pid <= 0 {
		return 0, false
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil || int(kp.Proc.P_pid) != pid {
		return 0, false
	}
	tv := kp.Proc.P_starttime
	return uint64(tv.Sec)*1_000_000 + uint64(tv.Usec), true
}

// ParentPID returns a process's parent pid from its kinfo_proc.
func ParentPID(pid int) (int, bool) {
	if pid <= 0 {
		return 0, false
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil || int(kp.Proc.P_pid) != pid {
		return 0, false
	}
	return int(kp.Eproc.Ppid), true
}

// EnvVar reads one variable of the environment process pid was started with,
// from kern.procargs2, which the kernel gives only for a process of the same
// user. A refusal reads as absent.
func EnvVar(pid int, name string) (string, bool) {
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return "", false
	}
	return ProcargsEnvVar(buf, name)
}

// Environ reads the environment process pid was started with, once, and
// false when it cannot be read.
func Environ(pid int) (Env, bool) {
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return Env{}, false
	}
	return Env{block: buf, procargs: true}, true
}

// StartedAt returns when process pid started, and false when it cannot be
// read.
func StartedAt(pid int) (time.Time, bool) {
	us, ok := StartTime(pid)
	if !ok {
		return time.Time{}, false
	}
	return time.UnixMicro(int64(us)), true
}

// SessionID returns the id of the process session pid is in, which is the
// pid of the session's leader, and false when the kernel does not say.
func SessionID(pid int) (int, bool) {
	sid, err := unix.Getsid(pid)
	return sid, err == nil && sid > 0
}

// SessionMembers lists the pids of the processes in session sid, as the
// kernel's process table says.
func SessionMembers(sid int) []int {
	if sid <= 0 {
		return nil
	}
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.session", sid)
	if err != nil {
		return nil
	}
	out := make([]int, 0, len(procs))
	for _, p := range procs {
		if pid := int(p.Proc.P_pid); pid > 0 {
			out = append(out, pid)
		}
	}
	return out
}
