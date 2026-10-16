//go:build windows || js

package session

import "os"

// fileIDOf has nothing to read here, where there is no handoff.
func fileIDOf(os.FileInfo) fileID { return fileID{} }
