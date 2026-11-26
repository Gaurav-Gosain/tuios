//go:build !unix && !windows

package main

import "errors"

// run reports that this system cannot start a command.
func run(string, []string) error {
	return errors.New("this system cannot start a command")
}
