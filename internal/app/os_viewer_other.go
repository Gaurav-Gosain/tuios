//go:build !unix

package app

import (
	"fmt"
	"os/exec"
)

// openInOSViewer hands a path to the shell's file association.
func openInOSViewer(path string) error {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not open %s: %w", path, err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
