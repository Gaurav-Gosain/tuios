//go:build darwin

package session

import (
	"os"
	"strings"
)

// dataVolume is where macOS mounts the volume that holds the person's data.
// Firmlinks show its folders (/Users, /private and others) at the top of the
// system volume too, and EvalSymlinks does not follow a firmlink.
const dataVolume = "/System/Volumes/Data"

// trimDataVolume is p by its short name when p is under /System/Volumes/Data
// and the short name is the same file. Without it a path through the data
// volume misses every deny entry and root, which are under the short names,
// and F_GETPATH names an open file by the short name.
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
