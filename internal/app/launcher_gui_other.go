//go:build !unix

package app

import "os/exec"

func detachProcess(*exec.Cmd) {}
