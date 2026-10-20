//go:build unix

package trust

import (
	"os"
	"syscall"
)

// openNoBlock opens path for reading without waiting on it. A FIFO with no
// writer would block a plain open forever, and the caller runs on the UI
// goroutine. The hygiene rules then read the descriptor and turn away
// anything that is not a regular file, where O_NONBLOCK changes nothing.
func openNoBlock(path string) (*os.File, error) {
	// #nosec G304 - the caller opens the tape or the store on purpose.
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
