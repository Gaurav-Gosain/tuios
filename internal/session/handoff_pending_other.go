//go:build !darwin && !linux

package session

func pendingBytes(int) int { return -1 }
