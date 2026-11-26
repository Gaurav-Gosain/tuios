package main

import (
	"errors"
	"os"
	"os/exec"
)

// run starts the command and exits with its code. Windows has no exec, so the
// report names this process, which lives as long as the command.
func run(bin string, args []string) error {
	cmd := exec.Command(bin, args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		os.Exit(exit.ExitCode())
	}
	if err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
