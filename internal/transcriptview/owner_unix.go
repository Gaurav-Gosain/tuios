//go:build unix

package transcriptview

import (
	"os"

	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

// serviceFileSafe reports whether service.json belongs to this user and no
// one else can write it: whoever can write it chooses where the password
// goes.
func serviceFileSafe(_ *os.File, info os.FileInfo) bool {
	return info.Mode().Perm()&0o022 == 0 && ownerIs(info, os.Getuid())
}

// ownerIs reports whether the file is owned by uid. An owner that cannot be
// read is never uid.
func ownerIs(info os.FileInfo, uid int) bool {
	owner, ok := procinfo.FileOwner(info)
	return ok && owner == uid
}
