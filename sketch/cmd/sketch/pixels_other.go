//go:build windows || js || plan9 || wasip1

package main

import "os"

func windowPixels(*os.File) (pw, ph, cols, rows int, ok bool) { return 0, 0, 0, 0, false }
