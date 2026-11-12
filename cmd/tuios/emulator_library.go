//go:build !ghostty

package main

// emulatorLibrary names the emulator library this binary linked, empty for the
// pure Go emulator, which links none.
func emulatorLibrary() string { return "" }
