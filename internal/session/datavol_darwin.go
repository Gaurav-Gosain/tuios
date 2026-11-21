//go:build darwin

package session

import (
	"os"
	"strings"
)

const dataVolume = "/System/Volumes/Data"

func trimDataVolume(p string) string {
	if !strings.HasPrefix(p, dataVolume+"/") {
		return p
	}
	short := strings.TrimPrefix(p, dataVolume)
	long, err := os.Stat(p)
	if err != nil {
		return p
	}
	if fi, err := os.Stat(short); err == nil && os.SameFile(long, fi) {
		return short
	}
	return p
}
