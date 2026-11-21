//go:build windows || js

package app

import "os/exec"

func detachFromTerminal(*exec.Cmd) {}

func killGroupOnCancel(*exec.Cmd) {}
