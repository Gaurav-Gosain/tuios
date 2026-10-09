//go:build !unix

package transcript

import "os"

// openNonBlocking is a plain open where there are no FIFOs to block on. The
// fstat in OpenRegular still refuses anything that is not a regular file.
func openNonBlocking(path string) (*os.File, error) {
	return os.Open(path) //nolint:gosec // the path came from the agent's own hook or from the manifest's directory
}

func isSymlinkLoop(error) bool { return false }

func setBlocking(*os.File) error { return nil }
