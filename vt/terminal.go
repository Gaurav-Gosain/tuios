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
//
// Only package vt implements Terminal, and it may gain methods in any
// release. Use it, and do not implement it: a type outside this package that
// implements it can stop compiling on an update. A program that needs its own
// implementation should depend on Screen, which stays small.
type Terminal interface {
	// Byte I/O and lifecycle. Write feeds raw PTY bytes; Read drains query
	// responses (DA, CPR, ...) the emulator wants sent back to the guest;
	// WriteResponse queues a response produced outside the emulator.
	Write(p []byte) (n int, err error)
	// Read drains the responses the emulator wants sent back to the guest.
	// It returns io.EOF after Close.
	Read(p []byte) (n int, err error)
	// TakeResponses removes and returns the queued responses without
	// blocking, or nil when there are none. A host that drains after each
	// Write uses it instead of a goroutine blocked in Read.
	TakeResponses() []byte
	// WriteResponse queues data to come out of Read, for a response made outside
	// the emulator.
	WriteResponse(data []byte)
	// Close stops the emulator and makes a blocked Read return io.EOF.
	Close() error
	// Resize sets the screen size in cells. A size below 1 counts as 1.
	Resize(width int, height int)

	// Grid reads. CellAt addresses the active screen, MainCellAt the main
	// screen even while the alternate screen is active. Both hand out a
	// pointer for reading only: a row nothing has printed on is served from
	// one shared blank cell, and a write through the pointer would show on
	// every blank cell of every pane. Writes go through SetCell.
	Width() int
	// Height returns the height of the screen in cells.
	Height() int
	// Bounds returns the screen as a rectangle at the origin.
	Bounds() uv.Rectangle
	// CellAt returns the cell at x, y of the active screen, for reading only.
	CellAt(x, y int) *uv.Cell
	// MainCellAt returns the cell at x, y of the main screen, for reading only.
	MainCellAt(x, y int) *uv.Cell
	// Render returns the active screen as text with its styles and links
	// encoded as escape sequences.
	Render() string
	// String returns the text of the active screen without styles.
	String() string
	// TailText returns the last n rows of the active screen that carry text,
	// in reading order. Blank rows are skipped.
	TailText(n int) []string

	// Grid writes, used only to prime an emulator from a wire snapshot.
	SetCell(x, y int, c *uv.Cell)
	// SetMainCell writes c at x, y of the main screen.
	SetMainCell(x, y int, c *uv.Cell)

	// Cursor.
	CursorPosition() uv.Position
	// IsCursorHidden reports whether the guest has hidden the cursor.
	IsCursorHidden() bool
	// CursorPen returns the style and hyperlink the next printed character
	// gets.
	CursorPen() (uv.Style, uv.Link)
	// CursorStyle returns the cursor shape DECSCUSR last set, and whether it
	// is steady rather than blinking.
	CursorStyle() (style CursorStyle, steady bool)
	// GuestCursorColor is the cursor colour the guest set with OSC 12 and
	// has not reset with OSC 112, or nil when it set none. Unlike
	// GuestColors it copies no palette, so a renderer can read it per frame.
	GuestCursorColor() color.Color
	// RestoreCursorPosition moves the cursor to x, y without side effects, to
	// prime an emulator from a snapshot.
	RestoreCursorPosition(x, y int)
	// CursorPendingWrap reports whether the cursor stands on the last column
	// with a wrap pending: the next printed character goes to the start of
	// the next row, not over the cell the cursor is on. The cursor position
	// alone cannot say this.
	CursorPendingWrap() bool
	// RestoreCursorPendingWrap arms or clears the pending wrap, after
	// RestoreCursorPosition has put the cursor back.
	RestoreCursorPendingWrap(pending bool)
	// RestoreCursorPen puts back the pen CursorPen returned, to prime an
	// emulator from a snapshot.
	RestoreCursorPen(pen uv.Style, link uv.Link)
	// RestoreCursorStyle puts back the shape CursorStyle returned, to prime an
	// emulator from a snapshot.
	RestoreCursorStyle(style CursorStyle, steady bool)
	// CursorProtected reports whether DECSCA protects what the guest prints
	// next. RestoreCursorProtected puts it back.
	CursorProtected() bool
	// RestoreCursorProtected sets the protection CursorProtected reports.
	RestoreCursorProtected(on bool)
	// SavedCursor is what DECSC last saved on the active screen, or with main
	// set on the main screen under an active alternate one. A screen that
	// never saved a cursor reports the zero SavedCursor, which is where a
	// DECRC with nothing saved goes. RestoreSavedCursor puts one back.
	SavedCursor(main bool) SavedCursor
	// RestoreSavedCursor puts back the cursor SavedCursor returned.
	RestoreSavedCursor(main bool, c SavedCursor)
	// LastPrinted is the character REP repeats, as the guest sent it, before
	// any character set maps it. It is empty when nothing has been printed
	// since the last reset. RestoreLastPrinted puts it back.
	LastPrinted() string
	// RestoreLastPrinted puts back the character LastPrinted returned.
	RestoreLastPrinted(cluster string)

	// Tab stops. TabStops lists the columns that hold a stop, in order.
	// RestoreTabStops replaces the table with the columns given, after any
	// resize, since a resize to a new width resets the table. A column off
	// the screen is dropped.
	TabStops() []int
	// RestoreTabStops sets a tab stop at each column in cols and clears the
	// others.
	RestoreTabStops(cols []int)

	// Titles is the window title, the icon name and what XTWINOPS 22 saved.
	// RestoreTitles puts all three back, keeping the newest entries of a
	// stack longer than XTWINOPS keeps. It does not tell the host through
	// the Title callback, except on the libghostty backend, whose library
	// takes a title only as OSC 2 and reports each one it takes.
	Titles() Titles
	// RestoreTitles puts back the titles Titles returned.
	RestoreTitles(t Titles)

	// GuestColors is what the guest set with OSC 4, 10, 11 and 12 and has
	// not reset. RestoreGuestColors replaces all of it.
	GuestColors() GuestColors
	// RestoreGuestColors replaces the colours the guest set with c.
	RestoreGuestColors(c GuestColors)

	// ANSIModes reports the ANSI (not DEC private) modes the emulator
	// implements, by number: insert mode (4) and newline mode (20) on both
	// backends. GetModes cannot carry them, because the two kinds share a
	// number space there. RestoreANSIModes sets them without side effects
	// and ignores a mode the emulator does not implement.
	ANSIModes() map[int]bool
	// RestoreANSIModes sets the ANSI modes ANSIModes returned.
	RestoreANSIModes(modes map[int]bool)

	// PendingInput is the input since the parser last left its ground
	// state: the start of an escape sequence or of a UTF-8 character that
	// has not ended yet, without a control the parser has already carried
	// out inside it. It is empty at ground and at most MaxPendingInput
	// bytes. A snapshot carries it, because the rest of the sequence
	// arrives after the snapshot and an emulator at ground prints it as
	// text. RestorePendingInput drops whatever unfinished input the
	// emulator holds and then reads p, so the next byte continues p.
	PendingInput() []byte
	// RestorePendingInput replaces the unfinished input with p.
	RestorePendingInput(p []byte)

	// Protected cells. ProtectedCells lists the cells DECSCA protected on
	// the active screen, or with main set on the main screen under an active
	// alternate one, as runs along a row. RestoreProtectedCells replaces the
	// protection on that screen with the runs given, after the cells are
	// written: SetCell leaves a cell unprotected.
	ProtectedCells(main bool) []CellRun
	// RestoreProtectedCells replaces the DECSCA protection of a screen with
	// runs.
	RestoreProtectedCells(main bool, runs []CellRun)

	// Screen and mode state. The Restore* half of each pair exists for the
	// same snapshot priming as SetCell.
	IsAltScreen() bool
	// ActiveScreenIsAlt reports whether the emulator reads and writes the
	// alternate screen. It is for diagnostics: IsAltScreen reports the mode.
	ActiveScreenIsAlt() bool
	// RestoreAltScreenMode makes the alternate or the main screen active
	// without clearing it and without changing the modes.
	RestoreAltScreenMode(enabled bool)
	// IsSyncActive reports whether the guest has a synchronized update (mode
	// 2026) open. An update held open too long reads as closed.
	IsSyncActive() bool
	// SyncUpdate reports what IsSyncActive does, and a serial that changes
	// each time the guest opens a new synchronized update.
	SyncUpdate() (open bool, serial uint64)
	// GetModes returns a copy of the DEC private modes the guest has set or
	// reset, by number.
	GetModes() map[int]bool
	// RestoreModes sets the DEC private modes GetModes returned, without the
	// side effects of a mode change.
	RestoreModes(modes map[int]bool)
	// ScrollRegion returns the margins the guest set with DECSTBM and DECSLRM,
	// or the whole screen.
	ScrollRegion() uv.Rectangle
	// RestoreScrollRegion puts back the margins ScrollRegion returned. An empty
	// r changes nothing.
	RestoreScrollRegion(r uv.Rectangle)
	// ResetScrollRegion makes the whole screen scroll.
	ResetScrollRegion()
	// Charsets returns the designator byte of the character set in each of G0
	// to G3, and which of them GL and GR select.
	Charsets() (ids [4]byte, gl, gr int)
	// RestoreCharsets puts back the selection Charsets returned.
	RestoreCharsets(ids [4]byte, gl, gr int)
	// ApplicationCursorKeys reports whether DECCKM is set, so cursor keys send
	// SS3 sequences.
	ApplicationCursorKeys() bool
	// BracketedPasteEnabled reports whether bracketed paste (mode 2004) is on.
	BracketedPasteEnabled() bool
	// FocusReportingEnabled reports whether focus reporting (mode 1004) is on.
	FocusReportingEnabled() bool

	// Soft wraps. RowSoftWrapped reports whether a row of the active screen
	// carries on to the next row because autowrap moved the text there, as
	// opposed to a line that happens to fill the row and then ends.
	// ScrollbackSoftWrapped is the same for a history line, oldest first.
	// known is false where the backend cannot tell, and a caller must then
	// treat the row as ending.
	RowSoftWrapped(y int) (wrapped, known bool)
	// ScrollbackSoftWrapped is RowSoftWrapped for history line index, oldest
	// first.
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
	// ScrollbackPadded is RowPadded for history line index, oldest first.
	ScrollbackPadded(index int) bool
	// RestorePads sets the padding flags RowPadded and ScrollbackPadded report.
	RestorePads(screen, history []bool)

	// Scrollback.
	ScrollbackLen() int
	// ScrollbackGeneration changes whenever the main screen's history does:
	// a line pushed, the ring trimmed, cleared or resized. A reader that
	// derived something from the history can keep it while the number
	// stays the same.
	ScrollbackGeneration() uint64
	// ScrollbackLine returns history line index, oldest first, or nil when
	// index is out of range.
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
	// PushScrollbackLine appends line to the main screen's history, to prime
	// an emulator from a snapshot.
	PushScrollbackLine(line uv.Line)
	// ClearScrollback empties the main screen's history.
	ClearScrollback()
	// SetScrollbackMaxLines sets how many lines the history keeps.
	SetScrollbackMaxLines(maxLines int)

	// Input encoding toward the guest.
	SendMouse(m Mouse)
	// EncodeMouseEvent returns the report the guest's mouse mode gives for m,
	// or "" when the guest does not want it.
	EncodeMouseEvent(m Mouse) string
	// SendMouseAt and EncodeMouseEventAt also take the pointer's pixel
	// position inside the pane, which a guest in SGR-pixel mode (1016) is
	// told instead of the cell centre.
	SendMouseAt(m Mouse, at MousePixel)
	// EncodeMouseEventAt is EncodeMouseEvent with the pixel position at.
	EncodeMouseEventAt(m Mouse, at MousePixel) string
	// HasMouseMode reports whether the guest has any mouse tracking mode on.
	HasMouseMode() bool
	// HasPixelMouseMode reports whether the guest tracks the mouse in
	// SGR-pixel mode (1016).
	HasPixelMouseMode() bool
	// HasAllMotionMode reports whether the guest tracks all mouse motion
	// (mode 1003).
	HasAllMotionMode() bool
	// HasCellMotionMode reports whether the guest tracks motion while a button
	// is pressed (mode 1002).
	HasCellMotionMode() bool
	// KittyKeyboardFlags returns the kitty keyboard protocol flags in effect.
	KittyKeyboardFlags() int
	// KittyKeyboardStack returns a copy of the active screen's kitty keyboard
	// flag stack, base entry first.
	KittyKeyboardStack() []int
	// RestoreKittyKeyboardState puts back the stack KittyKeyboardStack
	// returned, on the active screen. An empty stack changes nothing.
	RestoreKittyKeyboardState(stack []int)
	// KittyKeyboardMainStack is the main screen's flag stack while the
	// alternate screen is in use, nil otherwise. RestoreKittyKeyboardMainStack
	// puts it back.
	KittyKeyboardMainStack() []int
	// RestoreKittyKeyboardMainStack puts back the stack KittyKeyboardMainStack
	// returned. An empty stack changes nothing.
	RestoreKittyKeyboardMainStack(stack []int)
	// ModifyOtherKeys is the xterm modifyOtherKeys level the guest set with
	// XTMODKEYS (CSI > 4 ; n m): 0 off, 1 or 2. See EncodeModifyOtherKeys.
	ModifyOtherKeys() int
	// RestoreModifyOtherKeys puts back the level ModifyOtherKeys returned.
	// A level outside 0 to 2 changes nothing.
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
	// PaletteColor returns the colour of ANSI slot i (0 to 15) as it is drawn,
	// or nil for a slot out of range.
	PaletteColor(i int) color.Color
	// IndexedColor returns the colour of palette index i (0 to 255), or nil
	// for an index out of range.
	IndexedColor(i int) color.Color

	// Host hooks and graphics state.
	SetCallbacks(cb Callbacks)
	// GetCallbacks returns the callbacks SetCallbacks installed.
	GetCallbacks() Callbacks
	// SetScreenClearFunc sets the ScreenClear callback and keeps the others.
	SetScreenClearFunc(f func())
	// SetKittyPassthroughFunc installs the reader of every graphics
	// command. fn must not write to rawData: cmd.RawPayload shares its bytes.
	SetKittyPassthroughFunc(fn func(cmd *KittyCommand, rawData []byte))
	// SetKittyHeaderOnly says whether the passthrough acts on the control
	// keys alone, so the payload is not decoded. See ParseKittyHeader.
	SetKittyHeaderOnly(on bool)
	// SetKittyUploadAnswers says whether this emulator answers the guest's
	// kitty uploads and placements. A client emulator of a daemon pane does
	// not: the daemon's does. See KittyGuestImages.
	SetKittyUploadAnswers(on bool)
	// SetKittyFilesElsewhere says the pane runs on another machine, so a file
	// it names for an upload is refused without a look at it.
	SetKittyFilesElsewhere(on bool)
	// KittyGuestImagesState and RestoreKittyGuestImages carry the ids given
	// to kitty image numbers from the daemon to a client that attaches.
	KittyGuestImagesState() *KittyGuestImagesState
	// RestoreKittyGuestImages puts back the state KittyGuestImagesState
	// returned.
	RestoreKittyGuestImages(s *KittyGuestImagesState)
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
	// SetTextSizingFunc installs the function that takes each OSC 66 text
	// sizing sequence, with the cursor position, the scale and the length of
	// the text in characters.
	SetTextSizingFunc(fn func(rawOSC []byte, cursorX, cursorY, scale, textLen int))
	// SetReflowFunc sets a function a resize calls after it reflowed rows.
	// remap takes a row counted from the oldest history row, as an image
	// placement records it, and returns where that row's text is now. A
	// backend that cannot say never calls fn.
	SetReflowFunc(fn func(remap func(absLine int) int))
	// SetCellSize sets the size of one cell in pixels, for size reports and
	// SGR-pixel mouse reports. A size below 1 is ignored.
	SetCellSize(width, height int)
	// KittyMainState returns the kitty image state of the main screen.
	KittyMainState() *KittyState
	// KittyAltState returns the kitty image state of the alternate screen.
	KittyAltState() *KittyState
	// ReserveImageSpace moves the cursor below an image rows high, scrolling
	// as needed, so the next output does not cover the image.
	ReserveImageSpace(rows, cols int)
	// SemanticMarkers returns the OSC 133 prompt and command markers.
	SemanticMarkers() *SemanticMarkerList
}

var _ Terminal = (*Emulator)(nil)

// SavedCursor is the state DECSC saves and DECRC puts back: the position, the
// pen, the pending-wrap flag, origin mode, DECSCA protection and the character
// set selection. Link is the hyperlink the pen held, which the pure emulator
// saves with it and libghostty does not.
type SavedCursor struct {
	X, Y        int      // cursor position, zero-based
	Pen         uv.Style // rendition
	Link        uv.Link  // hyperlink the pen held
	PendingWrap bool     // wrap pending at the last column
	Origin      bool     // origin mode (DECOM)
	Protected   bool     // DECSCA protection
	// Charsets names the set in G0 to G3 by its designator byte, as
	// Charsets does. A zero byte reads as US ASCII.
	Charsets [4]byte
	GL, GR   int // the G sets invoked into GL and GR, 0 to 3
}

// CellRun is N cells of row Y from column X.
type CellRun struct {
	X, Y, N int // start column, row, and number of cells
}

// Titles is the window title, the icon name and the title stack XTWINOPS 22
// and 23 push to and pop from, oldest entry first.
type Titles struct {
	Title, Icon string       // window title and icon name
	Stack       []TitleEntry // saved titles, oldest first
}

// TitleEntry is one entry of the title stack. A push can save the title, the
// icon name or both, and a pop puts back only what the entry holds.
type TitleEntry struct {
	Title, Icon       string // saved title and icon name
	HasTitle, HasIcon bool   // whether the entry holds each
}

// GuestColors is what a guest set with OSC 4 (Palette, by slot) and with OSC
// 10, 11 and 12 (the default foreground, background and cursor colours). A
// nil entry is one the guest has not set, or has reset.
type GuestColors struct {
	Palette        [256]color.Color // OSC 4 colours by slot
	Fg, Bg, Cursor color.Color      // OSC 10, 11 and 12 colours
}

// MaxPendingInput bounds PendingInput: the longest payload the emulator keeps
// of one sequence, and room for its introducer and parameters. The parser
// cuts a longer payload at the same length, so input cut there replays to the
// same state.
const MaxPendingInput = maxSequenceData + 4096
