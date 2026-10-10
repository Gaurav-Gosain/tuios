//go:build windows

package session

import "syscall"

// oNoFollow is zero on Windows, where creating a symbolic link needs a
// privilege an ordinary user lacks.
const oNoFollow = 0

// oNonBlock is what the syscall package gives Windows; a named pipe there is
// not a path in a folder, so it changes nothing.
const oNonBlock = syscall.O_NONBLOCK
