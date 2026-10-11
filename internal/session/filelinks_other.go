//go:build !unix && !windows

package session

import "os"

// fileLinks cannot count names here, so every file has one.
func fileLinks(*os.File) (uint64, error) { return 1, nil }
