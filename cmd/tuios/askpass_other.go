//go:build !unix

package main

import (
	"errors"
	"os"
)

// foregroundTerminal finds no terminal here: every prompt goes to the daemon.
func foregroundTerminal() (*os.File, bool) { return nil, false }

// readHidden is never reached here.
func readHidden(*os.File) ([]byte, error) { return nil, errors.ErrUnsupported }
