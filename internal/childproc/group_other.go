//go:build !unix

package childproc

import "os/exec"

func ownGroup(*exec.Cmd) {}
