//go:build windows

package herdrplugin

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// detach starts the command suspended, so track can put it in a job before
// it runs a single instruction or starts a child of its own. CREATE_NO_WINDOW
// keeps a console program from opening a window: the daemon has no console
// to share, and the output goes to the plugin log.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW}
}

// useJobs is false only in a test of the path without a job.
var useJobs = true

// procGroup is a Windows job object holding the command and everything it
// starts. Windows has no process group to kill with one call; a job is the
// equivalent.
//
// While the command runs, the job kills what is in it when its last handle
// closes, so a plugin's processes stop with the daemon however the daemon
// ends, even when it is terminated and runs no cleanup of its own. Once the
// command exits, what it left behind is let go: a plugin that runs
// "cmd /c start msedge URL" or "code ." opened a program the person keeps.
// That matches Unix, where a finished command's group is not killed later.
type procGroup struct {
	cmd *exec.Cmd
	mu  sync.Mutex
	job windows.Handle // 0 when there is no job, or once it is let go
}

// track puts the started, suspended command in a new job and lets it run.
// Without a job the command still runs, and kill reaches the command alone.
// An error means the command could not be resumed; the caller kills it.
func track(cmd *exec.Cmd) (*procGroup, error) {
	g := &procGroup{cmd: cmd}
	pid := uint32(cmd.Process.Pid) //nolint:gosec // a process id fits
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME, false, pid)
	if err != nil {
		return g, fmt.Errorf("could not open the plugin process: %w", err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if useJobs {
		if job, err := newJob(); err == nil {
			if err := windows.AssignProcessToJobObject(job, h); err == nil {
				g.job = job
			} else {
				_ = windows.CloseHandle(job)
			}
		}
	}
	if err := ntResumeProcess(h); err != nil {
		return g, fmt.Errorf("could not resume the plugin process: %w", err)
	}
	return g, nil
}

// newJob makes a job that kills what is in it when it closes, and that
// lets a process start a child outside it with CREATE_BREAKAWAY_FROM_JOB.
func newJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	if err := setLimits(job, windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE|windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func setLimits(job windows.Handle, flags uint32) error {
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = flags
	_, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	return err
}

var procNtResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

// ntResumeProcess resumes every thread of a process started suspended. Go
// does not hand back the thread handle CreateProcess returned, and this
// needs only the process handle.
func ntResumeProcess(h windows.Handle) error {
	if err := procNtResumeProcess.Find(); err != nil {
		return err
	}
	r, _, _ := procNtResumeProcess.Call(uintptr(h))
	if r != 0 {
		return fmt.Errorf("NtResumeProcess: NTSTATUS 0x%08x", uint32(r))
	}
	return nil
}

// kill kills everything in the job, or the command alone without one. The
// lock is held across the call, so release cannot close the handle under
// it and a reused handle value cannot name another plugin's job.
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

// release lets the job go once the command is reaped. A process the command
// left behind keeps running: the kill-on-close limit is cleared first, and
// only then is the handle closed.
func (g *procGroup) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.job == 0 {
		return
	}
	if err := setLimits(g.job, 0); err != nil {
		// The limit stays, so closing the job would kill what is left.
		// The handle is left open instead: those processes then end with
		// the daemon, which is the lesser loss.
		g.job = 0
		return
	}
	_ = windows.CloseHandle(g.job)
	g.job = 0
}
