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
