//go:build !unix

package transcriptview

import (
	"os/exec"
	"time"
)

// killTreeOnCancel kills the command when its context ends, and stops
// waiting for its output soon after.
func killTreeOnCancel(cmd *exec.Cmd) { cmd.WaitDelay = 500 * time.Millisecond }

// portOwnedByMe cannot be shown here, so the password is never sent and the
// export is read instead.
func portOwnedByMe(int) bool { return false }
