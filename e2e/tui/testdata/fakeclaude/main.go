// Command fakeclaude is a program called claude that runs a shell script
// with its arguments and waits for it. It is how an E2E test puts a process
// with Claude Code's name in a pane's foreground, where the agent detector
// reads it, while a script drives the hooks: a script run directly is named
// after its interpreter.
package main

import (
	"errors"
	"os"
	"os/exec"
)

func main() {
	cmd := exec.Command("/bin/sh", os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			os.Exit(exit.ExitCode())
		}
		os.Exit(1)
	}
}
