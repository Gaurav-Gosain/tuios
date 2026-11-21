//go:build linux || darwin

package main

import (
	"os/exec"
	"runtime"
	"strings"
)

func keepAwakeMethodCheck() (method string, ok bool, detail string) {
	if runtime.GOOS == "darwin" {
		if _, err := exec.LookPath("/usr/bin/caffeinate"); err != nil {
			return "caffeinate", false, "/usr/bin/caffeinate is missing"
		}
		return "caffeinate", true, ""
	}
	path, err := exec.LookPath("systemd-inhibit")
	if err != nil {
		return "systemd-inhibit", false, "systemd-inhibit is not installed. This system may not run systemd"
	}
	out, err := exec.Command(path, "--list", "--no-pager").CombinedOutput() //nolint:gosec // a fixed command
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "systemd-inhibit", false, "logind does not answer: " + msg
	}
	return "systemd-inhibit", true, ""
}
