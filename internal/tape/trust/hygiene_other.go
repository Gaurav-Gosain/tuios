//go:build windows

package trust

import (
	"os"

	"github.com/Gaurav-Gosain/tuios/internal/units"
)

func ownedByCurrentUser(_ os.FileInfo) bool { return true }

func isGroupOrWorldAccessible(_ os.FileInfo) bool { return false }

func hygieneReason(info os.FileInfo) (string, bool) {
	if !info.Mode().IsRegular() {
		return "not a regular file", false
	}
	if info.Size() > MaxTapeSize {
		return "larger than " + units.Bytes(int64(MaxTapeSize)), false
	}
	return "", true
}
