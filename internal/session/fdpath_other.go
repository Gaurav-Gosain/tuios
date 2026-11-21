//go:build !linux && !darwin && !windows

package session

import "os"

func fdPath(*os.File) (string, bool) { return "", false }
