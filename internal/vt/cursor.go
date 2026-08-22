package vt

import uv "github.com/charmbracelet/ultraviolet"

// CursorStyle represents a cursor style.
type CursorStyle int

// Cursor styles.
const (
	CursorBlock CursorStyle = iota
	CursorUnderline
	CursorBar
)

// The shape a pane has before its guest says otherwise. Steady rather than
// blinking is what tuios has always shown, and a pane that has never seen a
// DECSCUSR must keep showing it, so this is pinned rather than left to a zero
// value.
const (
	defaultCursorStyle  = CursorBlock
	defaultCursorSteady = true
)

// Cursor represents a cursor in a terminal.
type Cursor struct {
	Pen  uv.Style
	Link uv.Link

	uv.Position

	Hidden bool
}
