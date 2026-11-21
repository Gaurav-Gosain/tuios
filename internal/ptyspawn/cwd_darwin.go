//go:build darwin

package ptyspawn

import (
	"context"

	"github.com/shirou/gopsutil/v4/process"
)

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
