//go:build !darwin

package session

// trimDataVolume leaves p as it is: only macOS has a data volume.
func trimDataVolume(p string) string { return p }
