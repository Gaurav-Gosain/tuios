package testutil

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

const watcherKind = "kqueues"

func watcherInstances() int {
	out, err := exec.Command("lsof", "-n", "-P", "-p", strconv.Itoa(os.Getpid()), "-F", "t").Output()
	if err != nil {
		return 0
	}
	return strings.Count(string(out), "\ntKQUEUE")
}

func startPoller() {
	if r, w, err := os.Pipe(); err == nil {
		_ = r.Close()
		_ = w.Close()
	}
}
