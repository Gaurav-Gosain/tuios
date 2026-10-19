//go:build windows || js

package main

import (
	"errors"
	"os"
	"time"

	"tuios.dev/sketch"
)

func windowPixels(*os.File) (pw, ph, cols, rows int, ok bool) { return 0, 0, 0, 0, false }

func readAnswer(*os.File, time.Duration) (string, error) { return "", errNoAnswer }

func runMermaidLive(string, mermaidOptions, liveOptions, *sketch.Theme, *os.File) error {
	return errors.New("--watch and --demo are not supported on this system")
}
