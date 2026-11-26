//go:build unix

package progresume

import (
	"io"
	"os"
	"syscall"
)

// openTTY opens the controlling terminal. The report goes there and not to
// standard output, which may be a pipe or a file.
func openTTY() (io.Writer, error) {
	return os.OpenFile("/dev/tty", os.O_WRONLY|syscall.O_NOCTTY, 0)
}
