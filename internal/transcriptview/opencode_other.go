//go:build !unix

package transcriptview

import (
	"os/exec"
	"time"
)

// killTreeOnCancel kills the command when its context ends, and stops
// waiting for its output soon after.
func killTreeOnCancel(cmd *exec.Cmd) { cmd.WaitDelay = 500 * time.Millisecond }
