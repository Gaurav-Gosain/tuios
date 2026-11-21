//go:build !unix

package session

import (
	"path/filepath"
	"strings"
)

func fsKey(dir string) string {
	if v := filepath.VolumeName(dir); v != "" {
		return "vol:" + strings.ToUpper(v)
	}
	return dir
}
