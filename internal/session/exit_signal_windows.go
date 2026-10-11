//go:build windows

package session

import "os"

// exitSignal is always 0 on Windows, where a process ends with a status and
// no signal.
func exitSignal(*os.ProcessState) int { return 0 }
