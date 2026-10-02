//go:build !linux && !darwin

package session

// readForegroundPGID reports no foreground process group on a platform with
// neither procfs nor the darwin sysctls.
func readForegroundPGID(int) (int, bool) { return 0, false }
