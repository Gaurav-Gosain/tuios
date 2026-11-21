//go:build unix

package session

import "syscall"

const openNoBlock = syscall.O_NONBLOCK
