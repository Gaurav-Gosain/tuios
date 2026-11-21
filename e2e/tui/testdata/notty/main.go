//go:build unix

// Command notty runs a program the way an agent's tool runs a command: in a
// session of its own, with no controlling terminal, stdin from /dev/null.
// It writes the program's stdout to OUT and its exit status to OUT.exit.
//
// Usage: notty OUT PROGRAM ARGS...
package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func main() {
	out, err := os.Create(os.Args[1])
	if err != nil {
		os.Exit(2)
	}
	cmd := exec.Command(os.Args[2], os.Args[3:]...)
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	code := 0
	if err := cmd.Run(); err != nil {
		code = 1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
	}
	_ = out.Close()
	_ = os.WriteFile(os.Args[1]+".exit", []byte(fmt.Sprint(code)), 0o600)
}
