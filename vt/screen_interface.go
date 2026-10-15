package vt

import (
	"io"

	uv "github.com/charmbracelet/ultraviolet"
)

// Screen is the part of an emulator most programs need: what a program
// prints goes in, the screen comes out, and the emulator's replies go back to
// the program. Terminal has everything tuios uses, and every Terminal is a
// Screen.
type Screen interface {
	// Write feeds the emulator what the program printed.
	io.Writer
	// Read returns the replies the emulator sends the program, such as the
	// answer to a cursor position query. It blocks until there is a reply,
	// and returns io.EOF after Close.
	Read(p []byte) (n int, err error)
	// Resize sets the screen size in cells.
	Resize(width, height int)
	// Width returns the width of the screen in cells.
	Width() int
	// Height returns the height of the screen in cells.
	Height() int
	// CellAt returns the cell at column x, row y, for reading only. It
	// returns nil outside the screen.
	CellAt(x, y int) *uv.Cell
	// CursorPosition returns the cursor's column and row.
	CursorPosition() uv.Position
	// Render returns the screen as text, with its styles and links as escape
	// sequences.
	Render() string
	// String returns the text of the screen without styles.
	String() string
	// Close stops the emulator and makes a blocked Read return io.EOF.
	Close() error
}

var (
	_ Screen = (*Emulator)(nil)
	_ Screen = Terminal(nil)
)
