//go:build unix

package app

import "syscall"

// openNonblock opens a file without waiting: an open of a named pipe for
// reading returns at once instead of waiting for a writer.
const openNonblock = syscall.O_NONBLOCK

// openNoFollow refuses to open a symlink in the last part of a path.
const openNoFollow = syscall.O_NOFOLLOW
