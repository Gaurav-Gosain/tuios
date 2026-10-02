//go:build unix

package app

import (
	"os/exec"
	"runtime"
)

// openInOSViewer hands a path to the desktop's own file handler. It is only
// ever called from a local client; on a remote one the viewer would open on
// the server, in front of nobody.
func openInOSViewer(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// The viewer outlives this call; nothing waits on it and nothing reads
	// back from it.
	go func() { _ = cmd.Wait() }()
	return nil
}
