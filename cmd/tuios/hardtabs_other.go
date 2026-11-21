//go:build !darwin && !dragonfly && !freebsd && !linux && !solaris && !aix

package main

func withoutHardTabs() func() { return func() {} }
