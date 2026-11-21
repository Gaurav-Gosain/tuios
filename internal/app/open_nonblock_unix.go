//go:build unix

package app

import "syscall"

const openNonblock = syscall.O_NONBLOCK

const openNoFollow = syscall.O_NOFOLLOW
