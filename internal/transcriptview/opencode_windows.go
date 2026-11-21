//go:build windows

package transcriptview

import (
	"os/exec"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/winjob"
)

func runTree(cmd *exec.Cmd) error {
	cmd.WaitDelay = 500 * time.Millisecond
	return winjob.RunTree(cmd)
}
