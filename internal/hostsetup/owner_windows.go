//go:build windows

package hostsetup

import "os"

// fileOwner has no answer on Windows, where sshd checks ACLs instead.
func fileOwner(os.FileInfo) (uint32, bool) { return 0, false }
