//go:build linux

package session

import (
	"net"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
	"golang.org/x/sys/unix"
)

const peerPIDSupported = true

func peerUID(conn net.Conn) (uid int, ok bool) {
	uc, isUnix := conn.(*net.UnixConn)
	if !isUnix {
		return 0, false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, false
	}
	_ = raw.Control(func(fd uintptr) {
		cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if err == nil && cred != nil {
			uid, ok = int(cred.Uid), true
		}
	})
	return uid, ok
}

func readProcLineage(pid int) (ppid int, tty int64, ok bool) {
	stat, ok := procinfo.Stat(pid)
	if !ok {
		return 0, 0, false
	}
	parent, ok := procinfo.StatField(stat, 4)
	if !ok {
		return 0, 0, false
	}
	nr, _ := procinfo.StatField(stat, 7)
	return parent, int64(nr), true
}

func readProcLink(pid int) (ppid int, tty int64, start uint64, ok bool) {
	stat, ok := procinfo.Stat(pid)
	if !ok {
		return 0, 0, 0, false
	}
	rparen := strings.LastIndexByte(stat, ')')
	if rparen < 0 {
		return 0, 0, 0, false
	}
	f := strings.Fields(stat[rparen+1:])
	if len(f) < 22-2 {
		return 0, 0, 0, false
	}
	parent, err := strconv.Atoi(f[4-3])
	if err != nil {
		return 0, 0, 0, false
	}
	nr, _ := strconv.ParseInt(f[7-3], 10, 64)
	st, err := strconv.ParseUint(f[22-3], 10, 64)
	if err != nil {
		return 0, 0, 0, false
	}
	return parent, nr, st, true
}
