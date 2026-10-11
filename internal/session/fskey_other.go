//go:build !unix

package session

import (
	"path/filepath"
	"strings"
)

// fsKey names the filesystem that holds dir, so writes to one disk are
// counted together: its volume here.
func fsKey(dir string) string {
	if v := filepath.VolumeName(dir); v != "" {
		return "vol:" + strings.ToUpper(v)
	}
	return dir
}
