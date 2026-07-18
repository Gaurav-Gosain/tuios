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
		// ansi.SgrPixelExtMouseMode,
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

	switch enc {
	// XXX: Support [ansi.HighlightMouseMode].
	// XXX: Support [ansi.Utf8ExtMouseMode], [ansi.UrxvtExtMouseMode], and
	// [ansi.SgrPixelExtMouseMode].
	case nil: // X10 mouse encoding
		_, _ = io.WriteString(e.pipe, ansi.MouseX10(b, mouse.X, mouse.Y))
	case ansi.ModeMouseExtSgr: // SGR mouse encoding
		_, _ = io.WriteString(e.pipe, ansi.MouseSgr(b, mouse.X, mouse.Y, isRelease))
	}
}
