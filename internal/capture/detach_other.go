//go:build !unix

package capture

import "os/exec"

func detach(*exec.Cmd) {}
