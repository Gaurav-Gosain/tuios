//go:build windows

package winjob

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// New makes a job with the given limit flags, such as
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE.
func New(limits uint32) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	if err := SetLimits(job, limits); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

// SetLimits replaces the limit flags of job.
func SetLimits(job windows.Handle, flags uint32) error {
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = flags
	_, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	return err
}

var procNtResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

// Resume resumes every thread of a process started suspended. Go does not
// hand back the thread handle CreateProcess returned, and this needs only
// the process handle.
func Resume(h windows.Handle) error {
	if err := procNtResumeProcess.Find(); err != nil {
		return err
	}
	r, _, _ := procNtResumeProcess.Call(uintptr(h))
	if r != 0 {
		return fmt.Errorf("NtResumeProcess: NTSTATUS 0x%08x", uint32(r))
	}
	return nil
}

// RunTree runs cmd, which has a context, in a job that holds it and every
// process it starts. When the context ends the whole job is killed, so a
// child cmd started cannot outlive a timeout. When cmd exits, what it left
// in the job is killed as well. The command starts suspended and runs only
// once it is in the job, so no child can start outside it. An error before
// the command runs means it was killed and nothing ran.
func RunTree(cmd *exec.Cmd) error {
	job, err := New(windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE)
	if err != nil {
		return fmt.Errorf("could not make a job: %w", err)
	}
	// Closing the last handle kills what is still in the job.
	defer func() { _ = windows.CloseHandle(job) }()
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	// The command itself is killed too: the context can end after Start and
	// before the command is in the job.
	cmd.Cancel = func() error {
		_ = windows.TerminateJobObject(job, 1)
		return cmd.Process.Kill()
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := enter(job, cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	return cmd.Wait()
}

// enter puts the suspended process pid in job and lets it run.
func enter(job windows.Handle, pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME,
		false, uint32(pid)) //nolint:gosec // a process id fits
	if err != nil {
		return fmt.Errorf("could not open the process: %w", err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		return fmt.Errorf("could not put the process in a job: %w", err)
	}
	return Resume(h)
}
