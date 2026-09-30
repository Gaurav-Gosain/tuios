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
	// screen even while the alternate screen is active. Both hand out a
	// pointer for reading only: a row nothing has printed on is served from
	// one shared blank cell, and a write through the pointer would show on
	// every blank cell of every pane. Writes go through SetCell.
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
	SyncUpdate() (open bool, serial uint64)
	GetModes() map[int]bool
	RestoreModes(modes map[int]bool)
	ScrollRegion() uv.Rectangle
	RestoreScrollRegion(r uv.Rectangle)
	ResetScrollRegion()
	Charsets() (ids [4]byte, gl, gr int)
	RestoreCharsets(ids [4]byte, gl, gr int)
	ApplicationCursorKeys() bool
	BracketedPasteEnabled() bool

	// Soft wraps. RowSoftWrapped reports whether a row of the active screen
	// carries on to the next row because autowrap moved the text there, as
	// opposed to a line that happens to fill the row and then ends.
	// ScrollbackSoftWrapped is the same for a history line, oldest first.
	// known is false where the backend cannot tell, and a caller must then
	// treat the row as ending.
	RowSoftWrapped(y int) (wrapped, known bool)
	ScrollbackSoftWrapped(index int) (wrapped, known bool)
	// RestoreSoftWraps sets the soft-wrap flags a snapshot carries, after
	// its cells are written: screen is one flag per row of the active screen,
	// and history one per row of the newest history rows, oldest first,
	// aligned to the end of the history. A row with no flag given reads as
	// ending, so a nil screen clears every screen row, and a nil history
	// clears the newest history row, the one that could carry on into a
	// screen the snapshot replaced. The screen under an active alternate
	// screen is cleared too: its flags do not travel.
	RestoreSoftWraps(screen, history []bool)

	// Scrollback.
	ScrollbackLen() int
	ScrollbackLine(index int) uv.Line
	PushScrollbackLine(line uv.Line)
	ClearScrollback()
	SetScrollbackMaxLines(maxLines int)

	// Input encoding toward the guest.
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
	// SetReportColors sets the colours an OSC 10 and OSC 11 query is answered
	// with while the guest has not set its own: the ground the pane is really
	// drawn on, when that is not the default. A nil colour keeps the default
	// answer. Nothing is drawn differently; it only changes the answer.
	SetReportColors(fg, bg color.Color)
	// SetReportPalette sets what an OSC 4 query for one of the sixteen ANSI
	// slots is answered with while neither the guest nor a theme has set that
	// slot: the host terminal's own colour, when tuios knows it. A nil entry
	// keeps the default answer. Like SetReportColors it changes only the
	// answer; the slot is still drawn by the host.
	SetReportPalette(pal [16]color.Color)
	PaletteColor(i int) color.Color
	IndexedColor(i int) color.Color

	// Host hooks and graphics state.
	SetCallbacks(cb Callbacks)
	GetCallbacks() Callbacks
	SetScreenClearFunc(f func())
	SetKittyPassthroughFunc(fn func(cmd *KittyCommand, rawData []byte))
	// SetKittyImageIDTranslator installs the guest-to-host image id mapping
	// used for kitty Unicode placeholder cells. See kitty_placeholder.go.
	SetKittyImageIDTranslator(fn KittyImageIDTranslator)
	// SetKittyPlaceholderMode says whether placeholder cells are kept or
	// dropped. See kitty_placeholder.go.
	SetKittyPlaceholderMode(m KittyPlaceholderMode)
	SetSixelPassthroughFunc(fn func(cmd *SixelCommand, cursorX, cursorY, absLine int))
	// SetSixelAdvertised installs the function that decides whether the
	// pane is told it can draw sixel: attribute 4 in the DA1 reply, and an
	// answer to XTSMGRAPHICS. Nil tells it nothing.
	SetSixelAdvertised(fn func() bool)
	SetTextSizingFunc(fn func(rawOSC []byte, cursorX, cursorY, scale, textLen int))
	SetCellSize(width, height int)
	KittyMainState() *KittyState
	KittyAltState() *KittyState
	ReserveImageSpace(rows, cols int)
	SemanticMarkers() *SemanticMarkerList
}

var _ Terminal = (*Emulator)(nil)
