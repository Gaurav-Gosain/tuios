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

// MousePixel is the pointer's position inside the pane in pixels, measured
// from the top-left pixel of the pane's first cell. A guest in SGR-pixel mode
// (DEC 1016) is told this position. OK is false when the host reported the
// pointer in cells only; the report then falls back to the cell centre.
type MousePixel struct {
	X, Y int
	OK   bool
}

// SendMouse sends a mouse event to the terminal. This can be any kind of mouse
// events such as [MouseClick], [MouseRelease], [MouseWheel], or [MouseMotion].
func (e *Emulator) SendMouse(m Mouse) {
	e.SendMouseAt(m, MousePixel{})
}

// SendMouseAt is SendMouse with the pointer's pixel position inside the pane,
// which a guest in SGR-pixel mode is told instead of the cell centre.
func (e *Emulator) SendMouseAt(m Mouse, at MousePixel) {
	r, mode, ok := e.mouseReportFor(m, at)
	if !ok {
		return
	}

	// Gate motion events on modes that actually support them.
	// Mode 1000/1001 (Normal/Highlight) only supports click/release.
	// Mode 1002 (ButtonEvent) supports motion while a button is pressed.
	// Mode 1003 (AnyEvent) supports all motion.
	if r.motion {
		switch mode {
		case ansi.ModeMouseX10, ansi.ModeMouseNormal, ansi.ModeMouseHighlight:
			// These modes don't support motion events at all
			return
		case ansi.ModeMouseButtonEvent:
			// CellMotion: only forward motion if a button is pressed
			if r.button == MouseNone {
				return
			}
		}
		// ModeMouseAnyEvent: forward all motion
	}

	if s := r.encode(); s != "" {
		_, _ = io.WriteString(e.pipe, s)
	}
}

// mouseReportFor builds the report for a mouse event from the guest's modes:
// the tracking mode in force, which is also returned, and the encoding. ok is
// false when the guest tracks no mouse at all.
//
// SGR-pixel (DEC mode 1016) takes precedence over every other encoding when the
// guest enabled it: a web page rendered by a kitty-graphics app (terminal-browser,
// awrit) probes 1016 and, once it sees it enabled, reads every mouse report as
// pixels. Reporting cell indices at that point places the pointer a cell-count of
// pixels from the origin, which is why hover and clicks land in the top-left. So
// when 1016 is set the cell position is scaled to host pixels.
//
// The pixel is the one the host reported (at), when the host reports pixels
// and tuios asked it to. Otherwise it is the cell centre, matching the
// cell->pixel convention a terminal app uses itself when it has only a cell
// report to work from.
func (e *Emulator) mouseReportFor(m Mouse, at MousePixel) (r mouseReport, mode ansi.Mode, ok bool) {
	for _, mm := range []ansi.DECMode{
		ansi.ModeMouseX10,         // Button press
		ansi.ModeMouseNormal,      // Button press/release
		ansi.ModeMouseHighlight,   // Button press/release/hilight
		ansi.ModeMouseButtonEvent, // Button press/release/cell motion
		ansi.ModeMouseAnyEvent,    // Button press/release/all motion
	} {
		if e.isModeSet(mm) {
			mode = mm
		}
	}
	if mode == nil {
		return r, nil, false
	}

	mouse := m.Mouse()
	_, r.motion = m.(MouseMotion)
	_, r.release = m.(MouseRelease)
	r.button = mouse.Button
	r.shift, r.alt, r.ctrl = mouse.Mod.Contains(ModShift), mouse.Mod.Contains(ModAlt), mouse.Mod.Contains(ModCtrl)
	r.x10Only = mode == ansi.ModeMouseX10
	r.encoding = pickMouseEncoding(
		e.isModeSet(ansi.ModeMouseExtUtf8),
		e.isModeSet(ansi.ModeMouseExtUrxvt),
		e.isModeSet(ansi.ModeMouseExtSgr),
		e.isModeSet(ansi.ModeMouseExtSgrPixel),
	)
	r.x, r.y = mouse.X, mouse.Y
	if r.encoding == mouseEncSGRPixel {
		if at.OK {
			r.x, r.y = at.X, at.Y
		} else {
			r.x, r.y = e.cellToPixel(mouse.X, mouse.Y)
		}
	}
	return r, mode, true
}

// cellToPixel maps a pane-relative cell position to the host pixel position at
// that cell's centre, for SGR-pixel (mode 1016) reporting. The cell dimensions
// are the host terminal's, set via SetCellSize from the detected capabilities.
func (e *Emulator) cellToPixel(cellX, cellY int) (int, int) {
	cw, ch := e.CellSize()
	return cellX*cw + cw/2, cellY*ch + ch/2
}
