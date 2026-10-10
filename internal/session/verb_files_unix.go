//go:build !windows && !js

package session

import "syscall"

// oNoFollow makes an open fail on a symbolic link, so a part file that
// someone replaced with a link is never written through it.
const oNoFollow = syscall.O_NOFOLLOW

// oNonBlock makes an open of a named pipe return at once instead of waiting
// for a writer.
const oNonBlock = syscall.O_NONBLOCK
