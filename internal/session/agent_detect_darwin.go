//go:build darwin

package session

import (
	"encoding/binary"
	"strings"

	"golang.org/x/sys/unix"
)

// The darwin half of the foreground-process resolver. macOS has no procfs, so
// every reader was returning nothing and no pane on a Mac was ever detected as
// running an agent, on a platform the project is used on daily.
//
// Two sysctls answer all four questions, and both are readable by an ordinary
// user for a process it owns, which is every process a tuios session spawned:
//
//   - kern.proc.pid gives a kinfo_proc, whose e_tpgid is the foreground process
//     group of the process's controlling terminal. It is the same number Linux
//     reports as field 8 of /proc/<pid>/stat, so the resolver above it is
//     unchanged.
//   - kern.procargs2 gives argc, then the path the kernel executed, then the
//     argument vector. The executable path comes with the arguments, so darwin
//     needs neither libproc nor cgo to see past a process that renamed itself,
//     which is the reading Claude Code's version-named binary depends on.
//
// Reading another user's process arguments is refused by the kernel rather than
// answered wrongly, and a refusal here leaves argv and exe empty. Detection then
// rests on comm alone, which is what a 16-byte truncated name can honestly
// support; nothing is guessed to fill the gap.

// readForegroundPGID returns the foreground process group of the process's
// controlling terminal, the e_tpgid of its kinfo_proc.
func readForegroundPGID(pid int) (int, bool) {
	if pid <= 0 {
		return 0, false
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil {
		return 0, false
	}
	tpgid := int(kp.Eproc.Tpgid)
	if tpgid <= 0 {
		return 0, false
	}
	return tpgid, true
}

// readProcessInfo reads the three descriptions of a process. comm is truncated at
// MAXCOMLEN by the kernel and rewritable by the process; exe and argv come from
// the same kern.procargs2 buffer, so they are consistent with each other even if
// the pid is reused between the two sysctls.
func readProcessInfo(pid int) foregroundInfo {
	if pid <= 0 {
		return foregroundInfo{}
	}
	info := foregroundInfo{}
	if kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid); err == nil && kp != nil {
		info.comm = cString(kp.Proc.P_comm[:])
	}
	info.exe, info.argv = readProcArgs(pid)
	return info
}

// readProcArgs reads kern.procargs2 for a pid and returns the executed path and
// the argument vector. Either may be empty when the sysctl is refused or the
// layout is not what is documented; neither is filled in by guessing.
//
// The buffer is: int32 argc, the NUL-terminated executable path, NUL padding to
// an alignment boundary, then argc NUL-terminated arguments, then the
// environment. Only the first two sections are read. The environment is not
// something process detection has any business looking at.
func readProcArgs(pid int) (string, []string) {
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(buf) < 4 {
		return "", nil
	}
	argc := int(int32(binary.LittleEndian.Uint32(buf[:4])))
	if argc <= 0 {
		return "", nil
	}
	rest := buf[4:]

	end := indexNUL(rest)
	if end < 0 {
		return "", nil
	}
	exe := string(rest[:end])
	rest = rest[end:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}

	argv := make([]string, 0, argc)
	for range argc {
		end := indexNUL(rest)
		if end < 0 {
			break
		}
		if arg := string(rest[:end]); arg != "" {
			argv = append(argv, arg)
		}
		rest = rest[end+1:]
	}
	return exe, argv
}

func indexNUL(b []byte) int {
	for i, c := range b {
		if c == 0 {
			return i
		}
	}
	return -1
}

// cString reads a NUL-terminated fixed-width kernel field.
func cString(b []byte) string {
	if i := indexNUL(b); i >= 0 {
		b = b[:i]
	}
	return strings.TrimSpace(string(b))
}
