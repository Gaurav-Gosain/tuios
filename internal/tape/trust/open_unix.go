//go:build unix

package trust

import (
	"os"
	"syscall"
)

func openNoBlock(path string) (*os.File, error) {
	// #nosec G304 - the caller opens the tape or the store on purpose.
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
