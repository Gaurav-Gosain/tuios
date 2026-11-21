//go:build !darwin

package session

func trimDataVolume(p string) string { return p }
