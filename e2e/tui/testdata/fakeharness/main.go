// Command fakeharness stands in for an agent harness that runs a hook: it
// starts its arguments as a child, with its own stdin, stdout, stderr and
// environment, waits for it and exits with its status. It is not a shell, so
// tuios agent-hook takes it for the harness and reports its pid as
// harness_pid, the way it takes Claude Code for the harness of its hooks.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: fakeharness command [args...]")
		os.Exit(2)
	}
	cmd := exec.Command(os.Args[1], os.Args[2:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			os.Exit(exit.ExitCode())
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
