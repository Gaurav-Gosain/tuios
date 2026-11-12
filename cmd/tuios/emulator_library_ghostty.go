//go:build ghostty

package main

import gh "go.mitchellh.com/libghostty"

// emulatorLibrary names the emulator library this binary linked and its
// version, read from the library at run time, so it reports the library that
// is in the binary and not the one the build meant to link.
func emulatorLibrary() string { return "libghostty-vt " + gh.Version() }
