//go:build !unix

package main

import (
	"errors"
	"os"
)

func ownTerminal(*os.File) (*os.File, error) {
	return nil, errors.New("no terminal file on this platform")
}
