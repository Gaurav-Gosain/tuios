//go:build windows

package transcriptview

import (
	"os/exec"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/winjob"
)

// runTree runs the export in a job object and kills the whole job when its
// context ends: opencode starts a private server under it, which would
// outlive a kill of the command alone. Windows has no process group, so the
// job is the group.
func runTree(cmd *exec.Cmd) error {
	cmd.WaitDelay = 500 * time.Millisecond
	return winjob.RunTree(cmd)
}
