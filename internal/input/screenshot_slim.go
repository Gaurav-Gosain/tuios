//go:build slim

package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// tuios-slim has no capture mode and no screenshot preview, so the gates in
// front of these never open. They stand in for capture_input.go and
// overlay_screenshot.go.

func HandleCaptureKey(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) { return o, nil }

func HandleScreenshotPreviewKey(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, nil
}

func handleCaptureMouseClick(_ tea.MouseClickMsg, o *app.OS) (*app.OS, tea.Cmd) { return o, nil }

func handleCaptureMouseMotion(_ tea.MouseMotionMsg, o *app.OS) (*app.OS, tea.Cmd) { return o, nil }

func handleCaptureMouseRelease(o *app.OS) (*app.OS, tea.Cmd) { return o, nil }

func handleScreenshotPreviewWheel(_ tea.MouseWheelMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, nil
}

// routeOverlayScreenshot never takes a key: there is no screenshot chord.
func routeOverlayScreenshot(tea.KeyPressMsg, *app.OS) (*app.OS, tea.Cmd, bool) {
	return nil, nil, false
}
