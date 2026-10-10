//go:build js

package session

import "os"

// The browser build has no links and no pipes on its disk, and its syscall
// package has neither flag.
const (
	oNoFollow = 0
	oNonBlock = 0
)

// syncData writes a file's bytes to the disk.
func syncData(f *os.File) error { return f.Sync() }
