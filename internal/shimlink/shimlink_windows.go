//go:build windows

package shimlink

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// EnsureDir creates a runtime directory.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	return nil
}

// Install makes <dir>/bin/<name> run exe and returns its path. A symbolic
// link needs Developer Mode or an administrator on Windows, so the file is a
// hardlink where it can be and a copy where it cannot. See InstallFile.
func Install(dir, name, exe string) (string, error) {
	bin := filepath.Join(dir, "bin")
	for _, d := range []string{dir, bin} {
		if err := EnsureDir(d); err != nil {
			return "", err
		}
	}
	link := filepath.Join(bin, name)
	if _, err := InstallFile(OSFS{}, link, exe, strconv.Itoa(os.Getpid())); err != nil {
		return "", err
	}
	return link, nil
}
