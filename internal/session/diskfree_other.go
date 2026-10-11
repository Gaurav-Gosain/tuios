//go:build !linux && !darwin && !windows

package session

// diskFree cannot read the free space here, so the reserve is not checked.
func diskFree(string) int64 { return -1 }
