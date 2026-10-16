//go:build !darwin && !linux

package session

// pendingBytes is unknown here.
func pendingBytes(int) int { return -1 }
