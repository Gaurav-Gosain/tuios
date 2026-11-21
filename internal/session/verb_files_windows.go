//go:build windows

package session

import (
	"os"
	"syscall"
)

const oNoFollow = 0

const oNonBlock = syscall.O_NONBLOCK

func syncData(f *os.File) error { return f.Sync() }
