//go:build !unix

package app

import (
	"fmt"
	"os/exec"
)

// The pixel tier and the OS viewer off unix. The preview's text tier is the
// whole panel here, which is the same complete panel every non-kitty terminal
// gets.

func (m *OS) screenshotGraphicsReady() bool { return false }

func (m *OS) clearScreenshotGraphics() {}

func (m *OS) flushScreenshotGraphicsForFrame() {}

// openInOSViewer hands a path to the shell's file association.
func openInOSViewer(path string) error {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not open %s: %w", path, err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
