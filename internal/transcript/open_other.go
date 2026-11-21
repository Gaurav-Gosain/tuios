//go:build !unix

package transcript

import "os"

func openNonBlocking(path string) (*os.File, error) {
	return os.Open(path) //nolint:gosec // the path came from the agent's own hook or from the manifest's directory
}

func isSymlinkLoop(error) bool { return false }

func setBlocking(*os.File) error { return nil }
