//go:build !unix && !windows

package transcriptview

import (
	"os/exec"
	"time"
)

func runTree(cmd *exec.Cmd) error {
	cmd.WaitDelay = 500 * time.Millisecond
	return cmd.Run()
}
