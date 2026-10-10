//go:build !windows && !js

package session

import (
	"os"
	"syscall"
)

// oNoFollow makes an open fail on a symbolic link, so a part file that
// someone replaced with a link is never written through it.
const oNoFollow = syscall.O_NOFOLLOW

// oNonBlock makes an open of a named pipe return at once instead of waiting
// for a writer.
const oNonBlock = syscall.O_NONBLOCK

// syncData writes a file's bytes to the disk with fsync. On macOS File.Sync
// asks the drive to empty its own cache too (F_FULLFSYNC), which takes tens
// of milliseconds; a folder of small files paid that per file. A plain fsync
// orders the write before the rename that puts the file in place, which is
// what a copy needs.
func syncData(f *os.File) error {
	for {
		err := syscall.Fsync(int(f.Fd()))
		if err != syscall.EINTR {
			return err
		}
	}
}
