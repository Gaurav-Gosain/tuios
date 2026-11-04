//go:build !windows

package tailscale

import "os/exec"

func hide(*exec.Cmd) {}
