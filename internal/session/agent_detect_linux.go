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
// the contents of a /proc/<pid>/stat line.
func parseStatTPGID(s string) (int, bool) { return parseStatField(s, 8) }

// parseStatPGRP extracts the pgrp (process group id, field 5).
func parseStatPGRP(s string) (int, bool) { return parseStatField(s, 5) }

// parseStatField extracts one numeric field, numbered as proc(5) numbers them,
// from the contents of a /proc/<pid>/stat line. The comm field (2) is wrapped
// in parentheses and may itself contain spaces or parentheses, so the numeric
// fields are parsed from after the final ')'.
func parseStatField(s string, field int) (int, bool) {
	rparen := strings.LastIndex(s, ")")
	if rparen < 0 || rparen+2 >= len(s) || field < 3 {
		return 0, false
	}
	// Fields after "(comm) ": state(3) ppid(4) pgrp(5) session(6) tty_nr(7)
	// tpgid(8). Splitting the remainder puts field n at index n-3.
	fields := strings.Fields(s[rparen+1:])
	if len(fields) < field-2 {
		return 0, false
	}
	v, err := strconv.Atoi(fields[field-3])
	if err != nil {
		return 0, false
	}
	return v, true
}

// readPGRP reads the process group of a pid, or 0 when it cannot be read.
func readPGRP(pid int) int {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	pgrp, _ := parseStatPGRP(string(data))
	return pgrp
}

// readChildren lists the children of a pid across all of its threads, from
// /proc/<pid>/task/<tid>/children. A child is listed under the thread that
// forked it, and a Go or Rust launcher forks from whichever thread was running,
// so reading only the main thread's list would miss the program a launcher
// started. A kernel built without CONFIG_PROC_CHILDREN has no such file, and the
// answer is then no children, which leaves detection reading the leader alone
// as it always did.
func readChildren(pid int) []int {
	dir := "/proc/" + strconv.Itoa(pid) + "/task"
	tids, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []int
	for i, tid := range tids {
		if i >= maxThreadsListed {
			break
		}
		data, err := os.ReadFile(dir + "/" + tid.Name() + "/children")
		if err != nil {
			continue
		}
		for f := range strings.FieldsSeq(string(data)) {
			if child, err := strconv.Atoi(f); err == nil {
				out = append(out, child)
			}
		}
	}
	return out
}

// maxThreadsListed bounds how many of a wrapper's threads are read for
// children. Every wrapper that matters has a handful; the bound is against a
// process with hundreds, which is not a wrapper.
const maxThreadsListed = 64

// foregroundGroup walks the descendants of leader that share its process group,
// depth first, reading at most limit processes no deeper than depth. Each
// yielded process carries its depth below the leader.
//
// The group is the honest boundary. A descendant in another process group is a
// background job or a daemon the wrapper started, and neither is what the pane
// is running in the foreground.
func foregroundGroup(leader, limit, depth int) func(yield func(foregroundInfo) bool) {
	return func(yield func(foregroundInfo) bool) {
		read := 0
		var walk func(pid, d int) bool
		walk = func(pid, d int) bool {
			if d > depth {
				return true
			}
			for _, child := range readChildren(pid) {
				if read >= limit {
					return false
				}
				if readPGRP(child) != leader {
					continue
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
