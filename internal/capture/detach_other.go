//go:build !unix

package capture

import "syscall"

// detachAttr has nothing to do off unix: the helpers there do not fork a
// selection owner.
func detachAttr() *syscall.SysProcAttr { return nil }
