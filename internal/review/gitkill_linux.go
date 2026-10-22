//go:build linux

package review

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// killTree is git's Cancel: it kills what git started, such as a
// git-remote-https that hangs on a remote, and then git. Left alone, such a
// child would run on after git is gone.
func killTree(cmd *exec.Cmd) func() error {
	return func() error {
		for _, pid := range descendants(cmd.Process.Pid, 0) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		return cmd.Process.Kill()
	}
}

// descendants lists pid's children and theirs, from /proc, deepest last.
func descendants(pid, depth int) []int {
	if depth > 8 {
		return nil
	}
	var out []int
	tasks, _ := os.ReadDir("/proc/" + strconv.Itoa(pid) + "/task")
	for _, t := range tasks {
		data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/task/" + t.Name() + "/children")
		if err != nil {
			continue
		}
		for f := range strings.FieldsSeq(string(data)) {
			if child, err := strconv.Atoi(f); err == nil {
				out = append(out, child)
				out = append(out, descendants(child, depth+1)...)
			}
		}
	}
	return out
}
