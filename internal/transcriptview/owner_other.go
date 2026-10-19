//go:build !unix

package transcriptview

import "os"

// fileOwner is the current user where the system keeps no uid. os.Getuid
// is -1 there, and the mode check is all that holds.
func fileOwner(os.FileInfo) int { return os.Getuid() }
