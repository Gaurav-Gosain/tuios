//go:build darwin

package terminal

import (
	"context"

	"github.com/shirou/gopsutil/v4/process"
)

// shellCWD asks the kernel through proc_pidinfo(PROC_PIDVNODEPATHINFO), which
// is darwin's answer to procfs for this question.
//
// gopsutil reaches it with purego rather than cgo, so this keeps the
// CGO_ENABLED=0 build the release uses, and gopsutil is already a dependency.
// It costs a couple of microseconds, which is well inside what the one second
// cache above it and the sidebar's own listing cadence can absorb.
//
// It answers only for a process the effective uid may inspect, which is the
// right boundary: every pane tuios owns is a child of this process, and a pid
// belonging to somebody else is one this build has no business reading.
func shellCWD(pgid int) (string, bool) {
	p, err := process.NewProcess(int32(pgid))
	if err != nil {
		return "", false
	}
	cwd, err := p.CwdWithContext(context.Background())
	if err != nil || cwd == "" {
		return "", false
	}
	return cwd, true
}
