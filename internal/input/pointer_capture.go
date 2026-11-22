package input

import (
	"strconv"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// Pointer capture (OSC 7731, see internal/terminal/pointer_capture.go).
//
// A pane normally hears the mouse only while the pointer is over its content,
// so a drag that leaves the pane stops at its edge. A remote desktop viewer
// that shows one display per pane needs more: a window dragged from the
// display in one pane to the display in the next has to keep moving, and the
// remote pointer has to follow the local one into the next display.
//
// So a program that asks for capture gets two things:
//
//   - A press in its pane makes the pane the owner of the drag. Every motion
//     and the release go to the owner until the button comes up, wherever
//     the pointer is, and focus stays where it is for the whole drag.
//   - While its pane has focus, it hears bare motion over the rest of the
//     screen too.
//
// Outside the pane the coordinates go on from the pane's first cell, so they
// can be below zero or past the pane's size. Only the SGR form can carry
// that, so a report outside the pane is always SGR (DEC 1006), in pixels when
// the program turned on SGR-pixel reports (DEC 1016). Inside the pane the
// report is the one any pane gets.
//
// A program that never asks gets exactly what it got before.

// pointerCaptureButton reports whether a press of b starts a captured drag:
// the left, middle and right buttons do, the wheel does not.
func pointerCaptureButton(b tea.MouseButton) bool {
	return b == tea.MouseLeft || b == tea.MouseMiddle || b == tea.MouseRight
}

// notePointerCapturePress makes w the owner of the drag that a press it was
// just sent starts, when its program asked for capture.
func notePointerCapturePress(o *app.OS, w *terminal.Window, b tea.MouseButton) {
	if w.PointerCapture() && pointerCaptureButton(b) {
		o.PointerCaptureOwner = w.ID
	}
}

// pointerCaptureOwner returns the pane that owns the drag in progress, or nil.
// An owner that closed, or that turned capture off, ends the drag: nothing is
// left to tell.
func pointerCaptureOwner(o *app.OS) *terminal.Window {
	if o.PointerCaptureOwner == "" {
		return nil
	}
	for _, w := range o.Windows {
		if w.ID == o.PointerCaptureOwner && w.Terminal != nil && w.PointerCapture() {
			return w
		}
	}
	o.PointerCaptureOwner = ""
	return nil
}

// pointerCaptureMotion sends motion to the owner of a captured drag. It
// reports whether it took the event. Motion with no button held means the
// release went missing, so the drag ends and the event goes on as usual.
func pointerCaptureMotion(o *app.OS, mouse tea.Mouse) bool {
	w := pointerCaptureOwner(o)
	if w == nil {
		return false
	}
	if mouse.Button == tea.MouseNone {
		o.PointerCaptureOwner = ""
		return false
	}
	if paneWantsMotion(o, w, mouse.Button) {
		sendCaptured(o, w, mouse.X, mouse.Y, uv.MouseMotionEvent{Button: uv.MouseButton(mouse.Button), Mod: uv.KeyMod(mouse.Mod)})
	}
	return true
}

// pointerCaptureRelease sends a release to the owner of a captured drag and
// ends the drag. It reports whether it took the event.
func pointerCaptureRelease(o *app.OS, mouse tea.Mouse) bool {
	w := pointerCaptureOwner(o)
	if w == nil {
		return false
	}
	o.PointerCaptureOwner = ""
	sendCaptured(o, w, mouse.X, mouse.Y, uv.MouseReleaseEvent{Button: uv.MouseButton(mouse.Button), Mod: uv.KeyMod(mouse.Mod)})
	return true
}

// pointerCaptureHover sends bare motion outside the focused pane to that
// pane, when its program asked for capture and tracks all motion. The motion
// still goes on to tuios's own hover.
func pointerCaptureHover(o *app.OS, w *terminal.Window, mouse tea.Mouse) {
	if mouse.Button != tea.MouseNone || !w.PointerCapture() || !paneWantsMotion(o, w, mouse.Button) {
		return
	}
	sendCaptured(o, w, mouse.X, mouse.Y, uv.MouseMotionEvent{Button: uv.MouseNone, Mod: uv.KeyMod(mouse.Mod)})
}

// sendCaptured sends ev, at screen cell (x, y), to w. Inside the content it
// goes the usual way. Outside it goes as an SGR report with coordinates past
// the edge.
func sendCaptured(o *app.OS, w *terminal.Window, x, y int, ev uv.MouseEvent) {
	tx, ty, inside := w.ScreenToTerminal(x, y)
	switch e := ev.(type) {
	case uv.MouseMotionEvent:
		e.X, e.Y = tx, ty
		ev = e
	case uv.MouseReleaseEvent:
		e.X, e.Y = tx, ty
		ev = e
	}
	if inside {
		sendMouseToWindow(o, w, ev)
		return
	}
	_ = w.SendInput(capturedReport(o, w, ev, tx, ty))
}

// capturedReport encodes ev at pane cell (tx, ty), which may be outside the
// pane, as an SGR report. Unlike ansi.MouseSgr it keeps the sign of a
// coordinate below zero.
func capturedReport(o *app.OS, w *terminal.Window, ev uv.MouseEvent, tx, ty int) []byte {
	m := ev.Mouse()
	_, motion := ev.(uv.MouseMotionEvent)
	_, release := ev.(uv.MouseReleaseEvent)
	b := ansi.EncodeMouseButton(m.Button, motion, m.Mod.Contains(uv.ModShift), m.Mod.Contains(uv.ModAlt), m.Mod.Contains(uv.ModCtrl))
	x, y := tx, ty
	if w.Terminal.HasPixelMouseMode() {
		at := o.PointerPixelIn(w.CellPixelWidth, w.CellPixelHeight, tx, ty)
		if at.OK {
			x, y = at.X, at.Y
		} else {
			cw, ch := w.CellPixelWidth, w.CellPixelHeight
			if cw <= 0 || ch <= 0 {
				cw, ch = 8, 16
			}
			x, y = tx*cw+cw/2, ty*ch+ch/2
		}
	}
	out := append([]byte("\x1b[<"), strconv.Itoa(int(b))...)
	out = append(out, ';')
	out = strconv.AppendInt(out, int64(x+1), 10)
	out = append(out, ';')
	out = strconv.AppendInt(out, int64(y+1), 10)
	if release {
		return append(out, 'm')
	}
	return append(out, 'M')
}
