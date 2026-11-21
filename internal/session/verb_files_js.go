//go:build js

package session

import "os"

const (
	oNoFollow = 0
	oNonBlock = 0
)

func syncData(f *os.File) error { return f.Sync() }
