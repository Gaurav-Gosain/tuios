//go:build windows

package herdrplugin

import (
	"os/exec"
	"sync"
	"syscall"

	"golang.org/x/sys/windows"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/winjob"
)

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW}
}

var useJobs = true

type procGroup struct {
	cmd *exec.Cmd
	mu  sync.Mutex
	job windows.Handle
}

func track(cmd *exec.Cmd) (*procGroup, error) {
	g := &procGroup{cmd: cmd}
	pid := uint32(cmd.Process.Pid) //nolint:gosec // a process id fits
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME, false, pid)
	if err != nil {
		return g, errfmt.Errorf("could not open the plugin process: %w", err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if useJobs {
		if job, err := winjob.New(windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK); err == nil {
			if err := windows.AssignProcessToJobObject(job, h); err == nil {
				g.job = job
			} else {
				_ = windows.CloseHandle(job)
			}
		}
	}
	if err := winjob.Resume(h); err != nil {
		return g, errfmt.Errorf("could not resume the plugin process: %w", err)
	}
	return g, nil
}

func (g *procGroup) kill() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.job != 0 {
		_ = windows.TerminateJobObject(g.job, 1)
		return
	}
	if g.cmd.Process != nil {
		_ = g.cmd.Process.Kill()
	}
}

func (g *procGroup) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.job == 0 {
		return
	}
	if err := winjob.SetLimits(g.job, 0); err != nil {
		g.job = 0
		return
	}
	_ = windows.CloseHandle(g.job)
	g.job = 0
}
