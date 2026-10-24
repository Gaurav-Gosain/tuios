//go:build !unix

package session

import "os"

// ownedByDaemonUser reports whether the folder at dir belongs to the user the
// daemon runs as. Where the system has no owner id to compare, the folder
// only has to be there; the home folder rule still keeps the system's
// folders out.
func ownedByDaemonUser(dir string) bool {
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}

// fileOwnedByDaemonUser reports whether the file fi describes belongs to the
// user the daemon runs as. Where the system has no owner id to compare, a
// file the daemon could open is taken as the user's.
func fileOwnedByDaemonUser(os.FileInfo) bool { return true }
