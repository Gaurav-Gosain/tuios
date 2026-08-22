package vt

import (
	"image/color"

	uv "github.com/charmbracelet/ultraviolet"
)

// Terminal is the emulator surface the rest of tuios consumes. It exists so
// the implementation can be swapped at build time (see New): the pure-Go
// Emulator is the default, a libghostty-vt backed implementation is available
// behind the ghostty build tag. Exactly one implementation is compiled into a
// binary; differential comparison between the two lives in tests only.
//
// The method set is the complete external surface of Emulator as of the
// extraction: every method here has at least one caller outside this package.
// Grow it only when a caller needs more, and implement additions on both
// backends in the same change.
type Terminal interface {
	// Byte I/O and lifecycle. Write feeds raw PTY bytes; Read drains query
	// responses (DA, CPR, ...) the emulator wants sent back to the guest;
	// WriteResponse queues a response produced outside the emulator.
	Write(p []byte) (n int, err error)
	Read(p []byte) (n int, err error)
	WriteResponse(data []byte)
	Close() error
	Resize(width int, height int)

	// Grid reads. CellAt addresses the active screen, MainCellAt the main
	// screen even while the alternate screen is active.
	Width() int
	Height() int
	Bounds() uv.Rectangle
	CellAt(x, y int) *uv.Cell
	MainCellAt(x, y int) *uv.Cell
	Render() string
	String() string
	TailText(n int) []string

	// Grid writes, used only to prime an emulator from a wire snapshot.
	SetCell(x, y int, c *uv.Cell)
	SetMainCell(x, y int, c *uv.Cell)

	// Cursor.
	CursorPosition() uv.Position
	IsCursorHidden() bool
	CursorPen() (uv.Style, uv.Link)
	CursorStyle() (style CursorStyle, steady bool)
	RestoreCursorPosition(x, y int)
	RestoreCursorPen(pen uv.Style, link uv.Link)
	RestoreCursorStyle(style CursorStyle, steady bool)

	// Screen and mode state. The Restore* half of each pair exists for the
	// same snapshot priming as SetCell.
	IsAltScreen() bool
	ActiveScreenIsAlt() bool
	RestoreAltScreenMode(enabled bool)
	IsSyncActive() bool
	GetModes() map[int]bool
	RestoreModes(modes map[int]bool)
	ScrollRegion() uv.Rectangle
	RestoreScrollRegion(r uv.Rectangle)
	ResetScrollRegion()
	Charsets() (ids [4]byte, gl, gr int)
	RestoreCharsets(ids [4]byte, gl, gr int)
	ApplicationCursorKeys() bool
	BracketedPasteEnabled() bool

	// Scrollback.
	ScrollbackLen() int
	ScrollbackLine(index int) uv.Line
	PushScrollbackLine(line uv.Line)
	ClearScrollback()
	SetScrollbackMaxLines(maxLines int)

	// Input encoding toward the guest.
	Paste(text string)
	SendMouse(m Mouse)
	EncodeMouseEvent(m Mouse) string
	HasMouseMode() bool
	HasAllMotionMode() bool
	HasCellMotionMode() bool
	KittyKeyboardFlags() int
	KittyKeyboardStack() []int
	RestoreKittyKeyboardState(stack []int)

	// Colors.
	SetThemeColors(fg, bg, cur color.Color, ansiPalette [16]color.Color)
	PaletteColor(i int) color.Color
	IndexedColor(i int) color.Color

	// Host hooks and graphics state.
	SetCallbacks(cb Callbacks)
	GetCallbacks() Callbacks
	SetScreenClearFunc(f func())
	SetKittyPassthroughFunc(fn func(cmd *KittyCommand, rawData []byte))
	SetSixelPassthroughFunc(fn func(cmd *SixelCommand, cursorX, cursorY, absLine int))
	SetTextSizingFunc(fn func(rawOSC []byte, cursorX, cursorY, scale, textLen int))
	SetCellSize(width, height int)
	KittyMainState() *KittyState
	KittyAltState() *KittyState
	ReserveImageSpace(rows, cols int)
	SemanticMarkers() *SemanticMarkerList
}

var _ Terminal = (*Emulator)(nil)
