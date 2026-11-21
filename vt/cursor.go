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

const (
	defaultCursorStyle  = CursorBlock
	defaultCursorSteady = true
)

// Cursor represents a cursor in a terminal.
type Cursor struct {
	Pen  uv.Style // style of the next printed character
	Link uv.Link  // hyperlink of the next printed character

	uv.Position

	Hidden bool // whether the guest hid the cursor (DECTCEM)
	// Protected says DECSCA protects what is printed next from a selective
	// erase. DECSC saves it with the rest of the cursor.
	Protected bool
}
