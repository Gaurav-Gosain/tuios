//go:build !unix && !windows

package session

import "os"

func fileLinks(*os.File) (uint64, error) { return 1, nil }
