package terminal

import "bytes"

// pointerCaptureOSC is the private OSC number a pane program uses to ask for
// pointer capture: "7731;tuios-pointer-capture;on" turns it on and "...;off"
// turns it off. While it is on, a drag that starts in the pane stays with the
// pane wherever the pointer goes, and the pane hears the pointer when it is
// outside the pane too, with coordinates past its edge. A remote desktop
// viewer that shows one display per pane uses it, so a window dragged from
// one display to the next keeps moving. See internal/input/pointer_capture.go.
//
// The emulator knows nothing of it and hands the body over through
// vt.Callbacks.PrivateOSC, as it does for the nvim navigator.
const pointerCaptureOSC = 7731

// pointerCaptureName is the second field of a pointer capture body.
const pointerCaptureName = "tuios-pointer-capture"

// parsePointerCapture reads a pointer capture body. on is the state it asks
// for, and ok is false for any other body. The number is optional, the same
// as for the nvim navigator.
func parsePointerCapture(data []byte) (on, ok bool) {
	parts := bytes.Split(data, []byte{';'})
	if len(parts) == 3 && string(parts[0]) == "7731" {
		parts = parts[1:]
	}
	if len(parts) != 2 || string(parts[0]) != pointerCaptureName {
		return false, false
	}
	switch string(parts[1]) {
	case "on":
		return true, true
	case "off":
		return false, true
	}
	return false, false
}

// PointerCapture reports whether the program in the pane asked for pointer
// capture and has not turned it off. Leaving the alternate screen turns it
// off too, so a program that exits without saying so does not leave its
// shell with it. Safe from any goroutine.
func (w *Window) PointerCapture() bool {
	return w != nil && w.pointerCapture.Load()
}
