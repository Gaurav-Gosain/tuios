//go:build darwin

package session

import (
	"encoding/binary"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

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

func readProcessInfo(pid int) foregroundInfo {
	if pid <= 0 {
		return foregroundInfo{}
	}
	info := foregroundInfo{}
	if kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid); err == nil && kp != nil {
		info.comm = cString(kp.Proc.P_comm[:])
	}
	info.exe, info.argv = readProcArgs(pid)
	info.exe = resolvedExe(info.exe)
	return info
}

func resolvedExe(exe string) string {
	if !filepath.IsAbs(exe) {
		return exe
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		return real
	}
	return exe
}

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

func foregroundGroup(leader, limit, depth int) func(yield func(foregroundInfo) bool) {
	return func(yield func(foregroundInfo) bool) {
		members, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", leader)
		if err != nil || len(members) == 0 {
			return
		}
		children := make(map[int][]int, len(members))
		for i := range members {
			pid, ppid := int(members[i].Proc.P_pid), int(members[i].Eproc.Ppid)
			if pid == leader {
				continue
			}
			children[ppid] = append(children[ppid], pid)
		}
		read := 0
		var walk func(pid, d int) bool
		walk = func(pid, d int) bool {
			if d > depth {
				return true
			}
			for _, child := range children[pid] {
				if read >= limit {
					return false
				}
				info := readProcessInfo(child)
				if info.comm == "" && len(info.argv) == 0 {
					continue
				}
				read++
				info.pid = child
				info.depth = d
				if !yield(info) {
					return false
				}
				if !walk(child, d+1) {
					return false
				}
			}
			return true
		}
		walk(leader, 1)
	}
}

func indexNUL(b []byte) int {
	for i, c := range b {
		if c == 0 {
			return i
		}
	}
	return -1
}

func cString(b []byte) string {
	if i := indexNUL(b); i >= 0 {
		b = b[:i]
	}
	return strings.TrimSpace(string(b))
}
