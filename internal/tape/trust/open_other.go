//go:build !unix

package trust

import "os"

func openNoBlock(path string) (*os.File, error) {
	// #nosec G304 - the caller opens the tape or the store on purpose.
	return os.Open(path)
}
