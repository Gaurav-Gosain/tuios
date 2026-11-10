//go:build !windows

package git

import "os/exec"

func hide(*exec.Cmd) {}
