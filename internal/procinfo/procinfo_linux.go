//go:build linux

package procinfo

import (
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// PeerPID returns the pid of the process on the other end of a unix socket,
// as the kernel recorded it at connect time (SO_PEERCRED), or 0 when it does
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
		if cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED); err == nil && cred != nil {
			pid = int(cred.Pid)
		}
	})
	return pid
}

// StartTime returns when process pid started, in clock ticks since boot
// (field 22 of /proc/<pid>/stat), and false when it cannot be read. It is
// parsed as a uint64 so a long uptime cannot overflow a 32-bit int.
func StartTime(pid int) (uint64, bool) {
	stat, ok := Stat(pid)
	if !ok {
		return 0, false
	}
	tok, ok := statToken(stat, 22)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseUint(tok, 10, 64)
	return v, err == nil
}

// ParentPID returns a process's parent pid, field 4 of /proc/<pid>/stat.
func ParentPID(pid int) (int, bool) {
	stat, ok := Stat(pid)
	if !ok {
		return 0, false
	}
	return StatField(stat, 4)
}

// EnvVar reads one variable of the environment process pid was started with.
// /proc/<pid>/environ is readable only for a process of the same user, and a
// refusal reads as absent.
func EnvVar(pid int, name string) (string, bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return "", false
	}
	return EnvironVar(data, name)
}

// Stat returns the contents of /proc/<pid>/stat, for StatField.
func Stat(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", false
	}
	return string(data), true
}

// StatField extracts one numeric field, numbered as proc(5) numbers them,
// from the contents of a /proc/<pid>/stat line.
func StatField(stat string, field int) (int, bool) {
	tok, ok := statToken(stat, field)
	if !ok {
		return 0, false
	}
	v, err := strconv.Atoi(tok)
	return v, err == nil
}

// statToken returns field number field of a /proc/<pid>/stat line. The comm
// field (2) is wrapped in parentheses and may itself contain spaces or
// parentheses, so fields are counted from after the final ')': state(3)
// ppid(4) pgrp(5) session(6) tty_nr(7) tpgid(8), field n at index n-3.
func statToken(stat string, field int) (string, bool) {
	rparen := strings.LastIndexByte(stat, ')')
	if rparen < 0 || rparen+2 >= len(stat) || field < 3 {
		return "", false
	}
	fields := strings.Fields(stat[rparen+1:])
	if len(fields) < field-2 {
		return "", false
	}
	return fields[field-3], true
}

// StartedAt returns when process pid started, as a wall clock time, and
// false when it cannot be read. Field 22 of /proc/<pid>/stat counts clock
// ticks since boot, and /proc/stat's btime says when boot was. A tick is
// USER_HZ, which Linux fixes at 100 for every program.
func StartedAt(pid int) (time.Time, bool) {
	ticks, ok := StartTime(pid)
	if !ok {
		return time.Time{}, false
	}
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, false
	}
	defer clear(data)
	for line := range strings.SplitSeq(string(data), "\n") {
		rest, ok := strings.CutPrefix(line, "btime ")
		if !ok {
			continue
		}
		boot, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
		if err != nil {
			return time.Time{}, false
		}
		const hz = 100
		return time.Unix(boot, 0).Add(time.Duration(ticks) * time.Second / hz), true
	}
	return time.Time{}, false
}
