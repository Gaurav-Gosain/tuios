//go:build !windows

package fang

import (
	"io"
)

func enableVirtualTerminalProcessing(io.Writer) error { return nil }
