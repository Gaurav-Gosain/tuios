//go:build !unix

package main

import (
	"errors"
	"os"
)

// ownTerminal has nothing to do here: sip gives a session on Windows no PTY
// slave file, so createTUIOSProgram never asks for one.
func ownTerminal(*os.File) (*os.File, error) {
	return nil, errors.New("no terminal file on this platform")
}
