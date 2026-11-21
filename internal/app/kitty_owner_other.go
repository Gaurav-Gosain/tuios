//go:build !unix

package app

import "os"

func ownedByMe(os.FileInfo) bool { return true }
