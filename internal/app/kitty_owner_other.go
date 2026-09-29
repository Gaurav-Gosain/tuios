//go:build !unix

package app

import "os"

// ownedByMe is true where file ownership is not a uid.
func ownedByMe(os.FileInfo) bool { return true }
