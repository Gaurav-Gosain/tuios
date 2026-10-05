//go:build darwin

package session

import (
	"encoding/binary"

	"golang.org/x/sys/unix"
)

// readParentAndOwner returns a process's parent pid and its effective user,
// from the kinfo_proc the kernel gives for it.
func readParentAndOwner(pid int) (ppid, uid int, ok bool) {
	if pid <= 0 {
		return 0, 0, false
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil || int(kp.Proc.P_pid) != pid {
		return 0, 0, false
	}
	return int(kp.Eproc.Ppid), int(kp.Eproc.Ucred.Uid), true
}

// readArgvExact returns a process's arguments with empty ones kept, from
// kern.procargs2. readProcArgs drops them, which is right for naming a process
// and wrong for replaying one: an empty value would shift the pairing of
// options and values.
func readArgvExact(pid int) []string {
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(buf) < 4 {
		return nil
	}
	argc := int(int32(binary.LittleEndian.Uint32(buf[:4])))
	if argc <= 0 {
		return nil
	}
	rest := buf[4:]
	end := indexNUL(rest)
	if end < 0 {
		return nil
	}
	rest = rest[end:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	argv := make([]string, 0, argc)
	for range argc {
		end := indexNUL(rest)
		if end < 0 {
			return nil
		}
		argv = append(argv, string(rest[:end]))
		rest = rest[end+1:]
	}
	return argv
}
