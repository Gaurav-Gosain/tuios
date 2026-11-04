//go:build unix

package session

import "syscall"

// openNoBlock keeps an open of a FIFO from blocking.
const openNoBlock = syscall.O_NONBLOCK
