//go:build !unix

package debuglog

import "os"

// openPrivate opens path for appending. There is no shared /tmp to guard on
// these platforms.
func openPrivate(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
}
