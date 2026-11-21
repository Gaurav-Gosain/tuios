//go:build !linux

package herdrplugin

import "os/exec"

func deathSignal(*exec.Cmd) {}
