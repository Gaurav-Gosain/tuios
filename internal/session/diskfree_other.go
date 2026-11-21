//go:build !linux && !darwin && !windows

package session

func diskFree(string) int64 { return -1 }
