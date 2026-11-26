package progresume

import (
	"io"
	"os"
)

// openTTY opens the console. The report goes there and not to standard
// output, which may be a pipe or a file.
func openTTY() (io.Writer, error) {
	return os.OpenFile("CONOUT$", os.O_WRONLY, 0)
}
