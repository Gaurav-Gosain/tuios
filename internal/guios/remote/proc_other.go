//go:build !windows

package remote

import "os/exec"

func hideWindow(*exec.Cmd) {}
