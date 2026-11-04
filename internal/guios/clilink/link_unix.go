//go:build !windows

package clilink

import (
	"os"
	"path/filepath"
)

func target(bin string) string {
	t, _ := os.Readlink(filepath.Join(bin, "kero"))
	return t
}

func link(exe, bin string) error {
	os.MkdirAll(bin, 0o755)
	link := filepath.Join(bin, "kero")
	if cur, err := os.Readlink(link); err == nil && cur == exe {
		return nil
	}
	os.Remove(link)
	return os.Symlink(exe, link)
}
