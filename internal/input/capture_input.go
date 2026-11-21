package input

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// HandleCaptureKey answers every key while capture mode is up. Nothing falls
// through: a mode that owns the screen owns the keyboard.
func HandleCaptureKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	switch commandKey(msg) {
	case "esc", "ctrl+c", "q":
		o.EndCapture()
		return o, nil
	case "f":
		o.EndCapture()
		return o, o.ScreenshotScreen()
	case "enter", "space":
		if o.CaptureOverOverlay() {
			o.EndCapture()
			return o, o.ScreenshotScreen()
		}
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
	switch commandKey(msg) {
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

func handleCaptureMouseClick(msg tea.MouseClickMsg, o *app.OS) (*app.OS, tea.Cmd) {
	mouse := msg.Mouse()
	if mouse.Button == tea.MouseRight {
		o.EndCapture()
		return o, nil
	}
	o.BeginCaptureDrag(mouse.X, mouse.Y)
	return o, nil
}

func handleCaptureMouseMotion(msg tea.MouseMotionMsg, o *app.OS) (*app.OS, tea.Cmd) {
	mouse := msg.Mouse()
	o.UpdateCapturePointer(mouse.X, mouse.Y, mouse.Button != tea.MouseNone)
	return o, nil
}

func handleCaptureMouseRelease(o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.FinishCaptureDrag()
}

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
