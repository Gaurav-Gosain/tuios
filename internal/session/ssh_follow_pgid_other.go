//go:build !unix

package session

import "os/exec"

func killGroupOnCancel(*exec.Cmd) {}
