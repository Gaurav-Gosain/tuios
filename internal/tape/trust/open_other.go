//go:build !unix

package trust

import "os"

// openNoBlock opens path for reading. Windows and the browser build have no
// FIFO a path open waits on, so a plain open does.
func openNoBlock(path string) (*os.File, error) {
	// #nosec G304 - the caller opens the tape or the store on purpose.
	return os.Open(path)
}
