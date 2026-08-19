//go:build linux

package session

import (
	"os"
	"strconv"
	"strings"
)

// The Linux half of the foreground-process resolver: everything here is procfs.
// See agent_detect.go for what the three readings are for, and
// agent_detect_darwin.go for the same four answers from sysctl.

// readForegroundPGID reads field 8 (tpgid) of /proc/<pid>/stat, the foreground
// process group id of the process's controlling terminal.
func readForegroundPGID(pid int) (int, bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	return parseStatTPGID(string(data))
}

// parseStatTPGID extracts the tpgid (foreground process group id, field 8) from
// the contents of a /proc/<pid>/stat line. The comm field (2) is wrapped in
// parentheses and may itself contain spaces or parentheses, so the numeric fields
// are parsed from after the final ')'.
func parseStatTPGID(s string) (int, bool) {
	rparen := strings.LastIndex(s, ")")
	if rparen < 0 || rparen+2 >= len(s) {
		return 0, false
	}
	// Fields after "(comm) ": state(3) ppid(4) pgrp(5) session(6) tty_nr(7)
	// tpgid(8). Splitting the remainder gives tpgid at index 5 (state at 0).
	fields := strings.Fields(s[rparen+1:])
	if len(fields) < 6 {
		return 0, false
	}
	tpgid, err := strconv.Atoi(fields[5])
	if err != nil {
		return 0, false
	}
	return tpgid, true
}

// readProcessInfo reads the three descriptions of a process from its procfs
// entry. Reading them from the same entry means a pid reused between the reads
// yields at worst a stale-but-consistent name for one tick.
func readProcessInfo(pid int) foregroundInfo {
	if pid <= 0 {
		return foregroundInfo{}
	}
	return foregroundInfo{
		comm: readComm(pid),
		argv: readCmdline(pid),
		exe:  readExe(pid),
	}
}

// readComm returns the trimmed contents of /proc/<pid>/comm, or "" on error.
func readComm(pid int) string {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// readCmdline returns the NUL-separated arguments of /proc/<pid>/cmdline as a
// slice, or nil on error or for a kernel thread (empty cmdline).
func readCmdline(pid int) []string {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil || len(data) == 0 {
		return nil
	}
	parts := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// readExe resolves /proc/<pid>/exe, the real binary behind a process whatever it
// renamed itself to, or "" when it cannot be read. A deleted binary resolves to a
// path with a " (deleted)" suffix, which is stripped so the name still matches.
func readExe(pid int) string {
	target, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(target, " (deleted)")
}
