//go:build !unix

package app

import (
	"os/exec"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

func (m *OS) screenshotGraphicsReady() bool { return false }

func (m *OS) clearScreenshotGraphics() {}

func (m *OS) flushScreenshotGraphicsForFrame() {}

func (m *OS) screenshotPreviewPictureBox() (inset, cols, rows int, ok bool) {
	return 0, 0, 0, false
}

func (m *OS) screenshotPreviewPixelBudget() (maxW, maxH int) { return 0, 0 }

func buildScreenshotTransmit(png []byte) []byte { return nil }

func openInOSViewer(path string) error {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	if err := cmd.Start(); err != nil {
		return errfmt.Errorf("could not open %s: %w", path, err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
