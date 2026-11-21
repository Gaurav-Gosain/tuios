//go:build windows || js

package session

import "os"

func fileIDOf(os.FileInfo) fileID { return fileID{} }

func fileUID(os.FileInfo) (int, bool) { return 0, false }
