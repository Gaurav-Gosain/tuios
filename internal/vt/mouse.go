package vt

import (
	"io"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// MouseButton represents the button that was pressed during a mouse message.
type MouseButton = uv.MouseButton

// Mouse event buttons
//
// This is based on X11 mouse button codes.
//
//	1 = left button
//	2 = middle button (pressing the scroll wheel)
//	3 = right button
//	4 = turn scroll wheel up
//	5 = turn scroll wheel down
//	6 = push scroll wheel left
//	7 = push scroll wheel right
//	8 = 4th button (aka browser backward button)
//	9 = 5th button (aka browser forward button)
//	10
//	11
//
// Other buttons are not supported.
const (
	MouseNone       = uv.MouseNone
	MouseLeft       = uv.MouseLeft
	MouseMiddle     = uv.MouseMiddle
	MouseRight      = uv.MouseRight
	MouseWheelUp    = uv.MouseWheelUp
	MouseWheelDown  = uv.MouseWheelDown
	MouseWheelLeft  = uv.MouseWheelLeft
	MouseWheelRight = uv.MouseWheelRight
	MouseBackward   = uv.MouseBackward
	MouseForward    = uv.MouseForward
	MouseButton10   = uv.MouseButton10
	MouseButton11   = uv.MouseButton11
)

// Mouse represents a mouse event.
type Mouse = uv.MouseEvent

// MouseClick represents a mouse click event.
type MouseClick = uv.MouseClickEvent

// MouseRelease represents a mouse release event.
type MouseRelease = uv.MouseReleaseEvent

// MouseWheel represents a mouse wheel event.
type MouseWheel = uv.MouseWheelEvent

// MouseMotion represents a mouse motion event.
type MouseMotion = uv.MouseMotionEvent

// SendMouse sends a mouse event to the terminal. This can be any kind of mouse
// events such as [MouseClick], [MouseRelease], [MouseWheel], or [MouseMotion].
func (e *Emulator) SendMouse(m Mouse) {
	// XXX: Support [Utf8ExtMouseMode], [UrxvtExtMouseMode], and
	// [SgrPixelExtMouseMode].
	var (
		enc  ansi.Mode
		mode ansi.Mode
	)

	for _, m := range []ansi.DECMode{
		ansi.ModeMouseX10,         // Button press
		ansi.ModeMouseNormal,      // Button press/release
		ansi.ModeMouseHighlight,   // Button press/release/hilight
		ansi.ModeMouseButtonEvent, // Button press/release/cell motion
		ansi.ModeMouseAnyEvent,    // Button press/release/all motion
	} {
		if e.isModeSet(m) {
			mode = m
		}
	}

	if mode == nil {
		return
	}

	// Gate motion events on modes that actually support them.
	// Mode 1000/1001 (Normal/Highlight) only supports click/release.
	// Mode 1002 (ButtonEvent) supports motion while a button is pressed.
	// Mode 1003 (AnyEvent) supports all motion.
	if _, isMotion := m.(MouseMotion); isMotion {
		switch mode {
		case ansi.ModeMouseX10, ansi.ModeMouseNormal, ansi.ModeMouseHighlight:
			// These modes don't support motion events at all
			return
		case ansi.ModeMouseButtonEvent:
			// CellMotion: only forward motion if a button is pressed
			if m.Mouse().Button == MouseNone {
				return
			}
		}
		// ModeMouseAnyEvent: forward all motion
	}

	for _, mm := range []ansi.DECMode{
		// ansi.Utf8ExtMouseMode,
		ansi.ModeMouseExtSgr,
		// ansi.UrxvtExtMouseMode,
	} {
		if e.isModeSet(mm) {
			enc = mm
		}
	}

	// Encode button
	mouse := m.Mouse()
	_, isMotion := m.(MouseMotion)
	_, isRelease := m.(MouseRelease)
	b := ansi.EncodeMouseButton(mouse.Button, isMotion,
		mouse.Mod.Contains(ModShift),
		mouse.Mod.Contains(ModAlt),
		mouse.Mod.Contains(ModCtrl))

	_, _ = io.WriteString(e.pipe, e.encodeMouseReport(enc, b, mouse.X, mouse.Y, isRelease))
}

// encodeMouseReport turns a pane-relative cell position into the wire form the
// guest asked for.
//
// SGR-pixel (DEC mode 1016) takes precedence over every other encoding when the
// guest enabled it: a web page rendered by a kitty-graphics app (terminal-browser,
// awrit) probes 1016 and, once it sees it enabled, reads every mouse report as
// pixels. Reporting cell indices at that point places the pointer a cell-count of
// pixels from the origin, which is why hover and clicks land in the top-left. So
// when 1016 is set the cell position is scaled to host pixels; otherwise the
// existing SGR-cell (1006) or X10 encoding is used unchanged.
//
// The pixel is the cell centre, matching the cell->pixel convention a terminal
// app uses itself when it has only a cell report to work from. Sub-cell
// precision is not available: the mouse position tuios receives from its own host
// is already quantised to cells, so a cell centre is the most accurate pixel it
// can report.
func (e *Emulator) encodeMouseReport(enc ansi.Mode, b byte, cellX, cellY int, isRelease bool) string {
	if e.isModeSet(ansi.ModeMouseExtSgrPixel) {
		px, py := e.cellToPixel(cellX, cellY)
		return ansi.MouseSgr(b, px, py, isRelease)
	}
	switch enc {
	// XXX: Support [ansi.HighlightMouseMode] and [ansi.Utf8ExtMouseMode],
	// [ansi.UrxvtExtMouseMode].
	case ansi.ModeMouseExtSgr: // SGR mouse encoding
		return ansi.MouseSgr(b, cellX, cellY, isRelease)
	default: // X10 mouse encoding
		return ansi.MouseX10(b, cellX, cellY)
	}
}

// cellToPixel maps a pane-relative cell position to the host pixel position at
// that cell's centre, for SGR-pixel (mode 1016) reporting. The cell dimensions
// are the host terminal's, set via SetCellSize from the detected capabilities.
func (e *Emulator) cellToPixel(cellX, cellY int) (int, int) {
	cw, ch := e.CellSize()
	return cellX*cw + cw/2, cellY*ch + ch/2
}
