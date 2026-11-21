//go:build !unix

package vt

import "os"

func kittyOwnedByMe(os.FileInfo) bool { return true }
