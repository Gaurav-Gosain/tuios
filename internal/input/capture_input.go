package input

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// Capture mode and preview-panel input.
//
// Capture mode is a gesture mode in the scrollback browser's mould: one
// boolean checked ahead of the mode split, so it owns the keyboard and the
// mouse while it is up, and one teardown that runs whether the gesture ended
// or was lost. It runs no timer of its own; every transition is an event that
// already woke the loop.

// HandleCaptureKey answers every key while capture mode is up. Nothing falls
// through: a mode that owns the screen owns the keyboard.
func HandleCaptureKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c", "q":
		o.EndCapture()
		return o, nil
	case "f":
		o.EndCapture()
		return o, o.ScreenshotScreen()
	case "enter", " ":
		idx := o.CaptureHover()
		o.EndCapture()
		return o, o.ScreenshotWindow(idx)
	case "tab", "right", "down", "j", "l":
		o.CaptureHoverNext(1)
		return o, nil
	case "shift+tab", "left", "up", "k", "h":
		o.CaptureHoverNext(-1)
		return o, nil
	}
	return o, nil
}

// HandleScreenshotPreviewKey answers every key while the preview is up. Each
// key here is one the panel's footer offers, and the footer only offers what
// works on this client.
func HandleScreenshotPreviewKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	switch msg.String() {
	case "enter", "q":
		o.CloseScreenshotPreview(false)
		return o, nil
	case "esc":
		o.CloseScreenshotPreview(true)
		return o, nil
	case "c":
		return o, o.CopyScreenshot()
	case "o":
		return o, o.OpenScreenshotFile()
	case "r":
		o.RetakeScreenshot()
		return o, nil
	case "down", "j":
		o.ScrollScreenshotPreview(0, 1)
		return o, nil
	case "up", "k":
		o.ScrollScreenshotPreview(0, -1)
		return o, nil
	case "right", "l":
		o.ScrollScreenshotPreview(4, 0)
		return o, nil
	case "left", "h":
		o.ScrollScreenshotPreview(-4, 0)
		return o, nil
	case "pgdown":
		o.ScrollScreenshotPreview(0, 10)
		return o, nil
	case "pgup":
		o.ScrollScreenshotPreview(0, -10)
		return o, nil
	case "home":
		o.ScrollScreenshotPreviewHome()
		return o, nil
	}
	return o, nil
}

// handleCaptureMouseClick starts a region drag and remembers which window is
// under the press, so a click that never moves captures that window.
func handleCaptureMouseClick(msg tea.MouseClickMsg, o *app.OS) (*app.OS, tea.Cmd) {
	mouse := msg.Mouse()
	if mouse.Button == tea.MouseRight {
		o.EndCapture()
		return o, nil
	}
	o.BeginCaptureDrag(mouse.X, mouse.Y)
	return o, nil
}

// handleCaptureMouseMotion tracks the drag, or the hover when no button is
// down. A hover lifts the window under the pointer so "click captures this" is
// visible before the click.
func handleCaptureMouseMotion(msg tea.MouseMotionMsg, o *app.OS) (*app.OS, tea.Cmd) {
	mouse := msg.Mouse()
	o.UpdateCapturePointer(mouse.X, mouse.Y, mouse.Button != tea.MouseNone)
	return o, nil
}

// handleCaptureMouseRelease finishes the gesture: a drag that covered more
// than a couple of cells is a region, anything smaller is a click on a window.
func handleCaptureMouseRelease(o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.FinishCaptureDrag()
}

// handleScreenshotPreviewWheel scrolls the preview body under the pointer.
func handleScreenshotPreviewWheel(msg tea.MouseWheelMsg, o *app.OS) (*app.OS, tea.Cmd) {
	mouse := msg.Mouse()
	switch mouse.Button {
	case tea.MouseWheelUp:
		o.ScrollScreenshotPreview(0, -3)
	case tea.MouseWheelDown:
		o.ScrollScreenshotPreview(0, 3)
	case tea.MouseWheelLeft:
		o.ScrollScreenshotPreview(-4, 0)
	case tea.MouseWheelRight:
		o.ScrollScreenshotPreview(4, 0)
	}
	return o, nil
}
