//go:build darwin

package session

import (
	"context"

	"github.com/shirou/gopsutil/v4/process"
)

// processCwd asks the kernel through proc_pidinfo(PROC_PIDVNODEPATHINFO),
// which is darwin's answer to procfs for this question.
//
// gopsutil reaches it with purego rather than cgo, so this keeps the
// CGO_ENABLED=0 build the release uses, and gopsutil is already a dependency
// for the same question in internal/terminal.
//
// Until this existed the session package read /proc on every platform, so on
// macOS it never had an answer: resurrection saved no directory and restored
// every pane into the daemon's own, and a new window had nothing to inherit.
func processCwd(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return "", false
	}
	cwd, err := p.CwdWithContext(context.Background())
	if err != nil || cwd == "" {
		return "", false
	}
	return cwd, true
}
