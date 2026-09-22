//go:build darwin

package ptyspawn

import (
	"context"

	"github.com/shirou/gopsutil/v4/process"
)

// ProcessCwd asks the kernel through proc_pidinfo(PROC_PIDVNODEPATHINFO),
// which is darwin's answer to procfs for this question.
//
// gopsutil reaches it with purego rather than cgo, so this keeps the
// CGO_ENABLED=0 build the release uses. It costs a couple of microseconds.
//
// It answers only for a process the effective uid may inspect, which is the
// right boundary: every pane tuios owns is a child of this process, and a pid
// belonging to somebody else is one this build has no business reading.
func ProcessCwd(pid int) (string, bool) {
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
