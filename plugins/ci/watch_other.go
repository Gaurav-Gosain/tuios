//go:build windows

package main

import (
	"errors"
	"os"
)

// tuios runs plugins on Linux and macOS. On Windows the watcher never
// starts, and the focus draws the cell alone.

func take(*os.File) bool { return false }

func held(*os.File) bool { return false }

func poke(int) error { return errors.New("no watcher on Windows") }

func onPoke(func()) {}
