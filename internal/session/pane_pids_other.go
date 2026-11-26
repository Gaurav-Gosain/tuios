//go:build !linux

package session

// zombie is false where the state cannot be read.
func zombie(int) bool { return false }
