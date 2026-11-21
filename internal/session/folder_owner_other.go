//go:build !unix

package session

import "os"

func ownedByDaemonUser(dir string) bool {
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}

func fileOwnedByDaemonUser(os.FileInfo) bool { return true }
