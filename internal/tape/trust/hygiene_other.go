//go:build windows

package trust

import (
	"fmt"
	"os"
)

// The tape hygiene preconditions model a POSIX threat (owner and group/other
// permission bits, world-writable shared directories). Windows has no
// equivalent of these bits through os.FileInfo, so the ownership and
// permission checks degrade to best-effort: the regular-file and size caps
// still apply, but ownership and writability cannot be asserted here.

func ownedByCurrentUser(_ os.FileInfo) bool { return true }

func isGroupOrWorldWritable(_ os.FileInfo) bool { return false }

func isGroupOrWorldAccessible(_ os.FileInfo) bool { return false }

func hygieneReason(info os.FileInfo) (string, bool) {
	if !info.Mode().IsRegular() {
		return "not a regular file", false
	}
	if info.Size() > MaxTapeSize {
		return fmt.Sprintf("larger than %d KiB", MaxTapeSize/1024), false
	}
	return "", true
}
