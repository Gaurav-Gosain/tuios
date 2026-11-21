//go:build linux

package session

import (
	"os"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

func readForegroundPGID(pid int) (int, bool) {
	stat, ok := procinfo.Stat(pid)
	if !ok {
		return 0, false
	}
	return procinfo.StatField(stat, 8)
}

func readPGRP(pid int) int {
	stat, _ := procinfo.Stat(pid)
	pgrp, _ := procinfo.StatField(stat, 5)
	return pgrp
}

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

const maxThreadsListed = 64

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

func readComm(pid int) string {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

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

func readExe(pid int) string {
	target, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(target, " (deleted)")
}
