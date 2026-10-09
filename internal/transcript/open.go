package transcript

import (
	"errors"
	"os"
)

// ErrNotRegular reports a transcript path that names something other than a
// regular file: a FIFO, a device, a directory or a symbolic link.
var ErrNotRegular = errors.New("transcript: not a regular file")

// OpenRegular opens a transcript for reading, and refuses anything that is
// not a regular file.
//
// A plain open of a FIFO blocks until a writer opens the other end, which
// for a FIFO nobody writes is forever, and it would block the goroutine that
// asked: the watcher's read, the join search on the agent tick, or a verb.
// So the file is opened without blocking and without following a symbolic
// link, checked with fstat on the descriptor it got, and only then put back
// in blocking mode for the reads. The check is on the descriptor, so a file
// swapped between a stat and the open cannot slip past it.
//
// A missing file is os.ErrNotExist, as from os.Open.
func OpenRegular(path string) (*os.File, error) {
	f, err := openNonBlocking(path)
	if err != nil {
		if isSymlinkLoop(err) {
			return nil, ErrNotRegular
		}
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, ErrNotRegular
	}
	if err := setBlocking(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}
