//go:build windows

package herdrplugin

import (
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// detach starts the command suspended, so track can put it in a job before
// it runs a single instruction or starts a child of its own.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
}

// procGroup is a Windows job object holding the command and everything it
// starts. Windows has no process group to kill with one call; a job is the
// equivalent. The job kills what is in it when its last handle closes, so
// a plugin's processes stop with the daemon however the daemon ends, even
// when it is terminated and runs no cleanup of its own.
type procGroup struct {
	cmd *exec.Cmd
	mu  sync.Mutex
	job windows.Handle // 0 when the job could not be made
}

// track puts the started, suspended command in a new job and lets it run.
// Without a job the command still runs, and kill reaches the command alone,
// as before. An error means the command could not be resumed; the caller
// kills it.
func track(cmd *exec.Cmd) (*procGroup, error) {
	g := &procGroup{cmd: cmd}
	pid := uint32(cmd.Process.Pid) //nolint:gosec // a process id fits
	if job, err := newKillOnCloseJob(); err == nil {
		if err := assign(job, pid); err == nil {
			g.job = job
		} else {
			_ = windows.CloseHandle(job)
		}
	}
	if err := resume(pid); err != nil {
		return g, fmt.Errorf("could not resume the plugin process: %w", err)
	}
	return g, nil
}

func newKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func assign(job windows.Handle, pid uint32) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	return windows.AssignProcessToJobObject(job, h)
}

// resume resumes the threads of a process started suspended: its one main
// thread. Go does not hand back the thread handle CreateProcess returned,
// so the thread is found by its process id.
func resume(pid uint32) error {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	var te windows.ThreadEntry32
	te.Size = uint32(unsafe.Sizeof(te))
	resumed := 0
	for err = windows.Thread32First(snap, &te); err == nil; err = windows.Thread32Next(snap, &te) {
		if te.OwnerProcessID != pid {
			continue
		}
		th, oerr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, te.ThreadID)
		if oerr != nil {
			return oerr
		}
		_, rerr := windows.ResumeThread(th)
		_ = windows.CloseHandle(th)
		if rerr != nil {
			return rerr
		}
		resumed++
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return err
	}
	if resumed == 0 {
		return errors.New("no thread found")
	}
	return nil
}

// kill kills everything in the job, or the command alone without one.
func (g *procGroup) kill() {
	g.mu.Lock()
	job := g.job
	g.mu.Unlock()
	if job != 0 {
		_ = windows.TerminateJobObject(job, 1)
		return
	}
	if g.cmd.Process != nil {
		_ = g.cmd.Process.Kill()
	}
}

// release closes the job once nothing in it runs, and reports whether it
// did. A process the command left behind keeps the job open, so StopPlugin
// and the daemon's exit still reach it, as a process group does on Unix.
func (g *procGroup) release() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.job == 0 {
		return true
	}
	var acct jobAccounting
	err := windows.QueryInformationJobObject(g.job, windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&acct)), uint32(unsafe.Sizeof(acct)), nil)
	if err == nil && acct.ActiveProcesses > 0 {
		return false
	}
	_ = windows.CloseHandle(g.job)
	g.job = 0
	return true
}

// drop closes the job whatever still runs in it, which kills what does.
func (g *procGroup) drop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.job != 0 {
		_ = windows.CloseHandle(g.job)
		g.job = 0
	}
}

// jobAccounting is JOBOBJECT_BASIC_ACCOUNTING_INFORMATION, which
// golang.org/x/sys/windows does not define.
type jobAccounting struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}
