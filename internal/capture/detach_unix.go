//go:build unix

package capture

import "syscall"

// detachAttr puts a helper in its own process group. xclip forks a server
// that must outlive the write; without this it dies with its parent and the
// clipboard is empty by the time anyone pastes.
func detachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
