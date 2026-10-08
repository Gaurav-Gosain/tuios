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
	// CursorPendingWrap reports whether the cursor stands on the last column
	// with a wrap pending: the next printed character goes to the start of
	// the next row, not over the cell the cursor is on. The cursor position
	// alone cannot say this.
	CursorPendingWrap() bool
	// RestoreCursorPendingWrap arms or clears the pending wrap, after
	// RestoreCursorPosition has put the cursor back.
	RestoreCursorPendingWrap(pending bool)
	RestoreCursorPen(pen uv.Style, link uv.Link)
	RestoreCursorStyle(style CursorStyle, steady bool)
	// CursorProtected reports whether DECSCA protects what the guest prints
	// next. RestoreCursorProtected puts it back.
	CursorProtected() bool
	RestoreCursorProtected(on bool)
	// SavedCursor is what DECSC last saved on the active screen, or with main
	// set on the main screen under an active alternate one. A screen that
	// never saved a cursor reports the zero SavedCursor, which is where a
	// DECRC with nothing saved goes. RestoreSavedCursor puts one back.
	SavedCursor(main bool) SavedCursor
	RestoreSavedCursor(main bool, c SavedCursor)
	// LastPrinted is the character REP repeats, as the guest sent it, before
	// any character set maps it. It is empty when nothing has been printed
	// since the last reset. RestoreLastPrinted puts it back.
	LastPrinted() string
	RestoreLastPrinted(cluster string)

	// Protected cells. ProtectedCells lists the cells DECSCA protected on
	// the active screen, or with main set on the main screen under an active
	// alternate one, as runs along a row. RestoreProtectedCells replaces the
	// protection on that screen with the runs given, after the cells are
	// written: SetCell leaves a cell unprotected.
	ProtectedCells(main bool) []CellRun
	RestoreProtectedCells(main bool, runs []CellRun)

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
	FocusReportingEnabled() bool

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
	// RowPadded and ScrollbackPadded report whether a wrapped row ended a
	// column early because a wide character did not fit in its last column,
	// so that column is padding and not text. RestorePads sets the flags a
	// snapshot carries, after RestoreSoftWraps, aligned the same way; it
	// only marks rows that are wrapped. A backend that does not track it
	// reports false and ignores the restore.
	RowPadded(y int) bool
	ScrollbackPadded(index int) bool
	RestorePads(screen, history []bool)

	// Scrollback.
	ScrollbackLen() int
	// ScrollbackGeneration changes whenever the main screen's history does:
	// a line pushed, the ring trimmed, cleared or resized. A reader that
	// derived something from the history can keep it while the number
	// stays the same.
	ScrollbackGeneration() uint64
	ScrollbackLine(index int) uv.Line
	// ScrollbackRows, ScrollbackText and CopyScrollback are for a reader of
	// many history lines at once. ScrollbackLine decodes each line into a
	// fresh line of 112-byte cells and keeps the newest in a cache for the
	// renderer, which a walk of the whole history churns and then pins.
	// These go round the cache.
	//
	// ScrollbackRows calls fn with each history line from index from to
	// end-1, oldest first, decoded to its full width into a buffer reused
	// from line to line: fn must copy what it keeps. It stops when fn
	// returns false. fn must not call the terminal, which may hold its own
	// lock while fn runs.
	ScrollbackRows(from, end int, fn func(index int, line uv.Line) bool)
	// ScrollbackText is ScrollbackRows for a reader that wants only the
	// text: each line is its cells' content and width, with no style and no
	// cell built. cells runs up to the line's last stored cell, and the
	// columns from len(cells) to width are spaces.
	ScrollbackText(from, end int, fn func(index, width int, cells []TextCell) bool)
	// CopyScrollback copies history lines from index from to end-1, with
	// their wrap and padding flags, into a form a reader decodes after it
	// releases the terminal.
	CopyScrollback(from, end int) *ScrollbackCopy
	PushScrollbackLine(line uv.Line)
	ClearScrollback()
	SetScrollbackMaxLines(maxLines int)

	// Input encoding toward the guest.
	SendMouse(m Mouse)
	EncodeMouseEvent(m Mouse) string
	// SendMouseAt and EncodeMouseEventAt also take the pointer's pixel
	// position inside the pane, which a guest in SGR-pixel mode (1016) is
	// told instead of the cell centre.
	SendMouseAt(m Mouse, at MousePixel)
	EncodeMouseEventAt(m Mouse, at MousePixel) string
	HasMouseMode() bool
	// HasPixelMouseMode reports whether the guest tracks the mouse in
	// SGR-pixel mode (1016).
	HasPixelMouseMode() bool
	HasAllMotionMode() bool
	HasCellMotionMode() bool
	KittyKeyboardFlags() int
	KittyKeyboardStack() []int
	RestoreKittyKeyboardState(stack []int)
	// KittyKeyboardMainStack is the main screen's flag stack while the
	// alternate screen is in use, nil otherwise. RestoreKittyKeyboardMainStack
	// puts it back.
	KittyKeyboardMainStack() []int
	RestoreKittyKeyboardMainStack(stack []int)
	// ModifyOtherKeys is the xterm modifyOtherKeys level the guest set with
	// XTMODKEYS (CSI > 4 ; n m): 0 off, 1 or 2. See EncodeModifyOtherKeys.
	ModifyOtherKeys() int
	RestoreModifyOtherKeys(level int)

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
	// SetKittyPassthroughFunc installs the reader of every graphics
	// command. fn must not write to rawData: cmd.RawPayload shares its bytes.
	SetKittyPassthroughFunc(fn func(cmd *KittyCommand, rawData []byte))
	// SetKittyHeaderOnly says whether the passthrough acts on the control
	// keys alone, so the payload is not decoded. See ParseKittyHeader.
	SetKittyHeaderOnly(on bool)
	// SetKittyImageIDTranslator installs the guest-to-host image id mapping
	// used for kitty Unicode placeholder cells. See kitty_placeholder.go.
	SetKittyImageIDTranslator(fn KittyImageIDTranslator)
	// SetKittyPlaceholderMode says whether placeholder cells are kept or
	// dropped. See kitty_placeholder.go.
	SetKittyPlaceholderMode(m KittyPlaceholderMode)
	// SetSixelPassthroughFunc installs the function that takes a guest's
	// sixel image and returns the id its cells are marked with. See
	// sixel_marker.go.
	SetSixelPassthroughFunc(fn SixelPassthroughFunc)
	// SetSixelAdvertised installs the function that decides whether the
	// pane is told it can draw sixel: attribute 4 in the DA1 reply, and an
	// answer to XTSMGRAPHICS. Nil tells it nothing.
	SetSixelAdvertised(fn func() bool)
	SetTextSizingFunc(fn func(rawOSC []byte, cursorX, cursorY, scale, textLen int))
	// SetReflowFunc sets a function a resize calls after it reflowed rows.
	// remap takes a row counted from the oldest history row, as an image
	// placement records it, and returns where that row's text is now. A
	// backend that cannot say never calls fn.
	SetReflowFunc(fn func(remap func(absLine int) int))
	SetCellSize(width, height int)
	KittyMainState() *KittyState
	KittyAltState() *KittyState
	ReserveImageSpace(rows, cols int)
	SemanticMarkers() *SemanticMarkerList
}

var _ Terminal = (*Emulator)(nil)

// SavedCursor is the state DECSC saves and DECRC puts back: the position, the
// pen, the pending-wrap flag, origin mode, DECSCA protection and the character
// set selection. Link is the hyperlink the pen held, which the pure emulator
// saves with it and libghostty does not.
type SavedCursor struct {
	X, Y        int
	Pen         uv.Style
	Link        uv.Link
	PendingWrap bool
	Origin      bool
	Protected   bool
	// Charsets names the set in G0 to G3 by its designator byte, as
	// Charsets does. A zero byte reads as US ASCII.
	Charsets [4]byte
	GL, GR   int
}

// CellRun is N cells of row Y from column X.
type CellRun struct {
	X, Y, N int
}
