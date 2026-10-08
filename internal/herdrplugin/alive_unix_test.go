//go:build unix

package herdrplugin

import "syscall"

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func killPid(pid int) { _ = syscall.Kill(pid, syscall.SIGKILL) }
