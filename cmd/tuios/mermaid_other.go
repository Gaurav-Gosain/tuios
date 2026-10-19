//go:build windows || js

package main

import (
	"os"
	"time"
)

func windowPixels(*os.File) (pw, ph, cols, rows int, ok bool) { return 0, 0, 0, 0, false }

func readAnswer(*os.File, time.Duration) (string, error) { return "", errNoAnswer }
