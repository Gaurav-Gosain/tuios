//go:build !unix

package main

import "os/exec"

// buildGroup leaves the command as it is. A timeout kills the build
// command, and the processes it started can run on.
func buildGroup(*exec.Cmd) {}
