//go:build !linux

package session

import (
	"os"
	"os/exec"
)

const execByDescriptor = false

func openBinary(string) (*os.File, error) { return nil, nil }

func (handoffBinary) runOpenFile(*exec.Cmd) {}

func (b handoffBinary) execPath() string { return b.path }

func (handoffBinary) isScript() bool { return false }

func setProcessName(string) {}
