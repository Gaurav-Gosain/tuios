//go:build windows

package shimlink

import (
	"errors"
	"fmt"
	"os"
)

// EnsureDir creates a runtime directory.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	return nil
}

// Install is not supported on Windows, where a link needs a privilege a
// person rarely holds.
func Install(dir, name, exe string) (string, error) {
	return "", errors.New("the " + name + " link is not supported on Windows")
}
