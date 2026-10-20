//go:build !unix && !windows

package transcriptview

import (
	"os/exec"
	"time"
)

// runTree runs the command, kills it when its context ends, and stops
// waiting for its output soon after.
func runTree(cmd *exec.Cmd) error {
	cmd.WaitDelay = 500 * time.Millisecond
	return cmd.Run()
}
