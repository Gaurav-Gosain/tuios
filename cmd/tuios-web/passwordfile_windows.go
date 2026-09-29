//go:build windows

package main

import "os"

// checkPasswordFileMode does nothing on Windows. Windows has no owner and mode
// bits of this kind, so tuios-web does not check who can read the file there.
func checkPasswordFileMode(string, os.FileInfo, int) error { return nil }
