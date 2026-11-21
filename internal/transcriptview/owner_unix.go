//go:build unix

package transcriptview

import (
	"os"

	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

func serviceFileSafe(_ *os.File, info os.FileInfo) bool {
	return info.Mode().Perm()&0o022 == 0 && ownerIs(info, os.Getuid())
}

func ownerIs(info os.FileInfo, uid int) bool {
	owner, ok := procinfo.FileOwner(info)
	return ok && owner == uid
}
