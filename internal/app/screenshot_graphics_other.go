//go:build !unix && !slim

package app

// The pixel tier and the OS viewer off unix. The preview's text tier is the
// whole panel here, which is the same complete panel every non-kitty terminal
// gets.

func (m *OS) screenshotGraphicsReady() bool { return false }

func (m *OS) clearScreenshotGraphics() {}

func (m *OS) flushScreenshotGraphicsForFrame() {}

// screenshotPreviewPictureBox never has a picture to report here, so the panel
// draws its cells over the whole body.
func (m *OS) screenshotPreviewPictureBox() (inset, cols, rows int, ok bool) {
	return 0, 0, 0, false
}

// screenshotPreviewPixelBudget is zero here, which is what tells the render
// command not to shrink and encode a preview picture nothing would place.
func (m *OS) screenshotPreviewPixelBudget() (maxW, maxH int) { return 0, 0 }

func buildScreenshotTransmit(png []byte) []byte { return nil }
