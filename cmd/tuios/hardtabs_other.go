//go:build !darwin && !dragonfly && !freebsd && !linux && !solaris && !aix

package main

// withoutHardTabs is a no-op where bubbletea does not read TABDLY. See the
// unix version.
func withoutHardTabs() func() { return func() {} }
