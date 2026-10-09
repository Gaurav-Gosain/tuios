//go:build !windows

package session

import "syscall"

// oNoFollow makes an open fail on a symbolic link, so a part file that
// someone replaced with a link is never written through it.
const oNoFollow = syscall.O_NOFOLLOW
