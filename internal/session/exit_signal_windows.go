//go:build windows

package session

import "os"

func exitSignal(*os.ProcessState) int { return 0 }
