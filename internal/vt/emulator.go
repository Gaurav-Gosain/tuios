package vt

import (
	"image/color"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/ultraviolet/screen"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/parser"
)

// Logger represents a logger interface.
type Logger interface {
	Printf(format string, v ...any)
}

// Emulator represents a virtual terminal emulator.
type Emulator struct {
	handlers

	// The terminal's indexed 256 colors.
	colors [256]color.Color

	// Both main and alt screens and a pointer to the currently active screen.
	scrs [2]Screen
	scr  *Screen

	// Character sets, and the designator byte each was selected by. The sets
	// themselves are maps and cannot be compared back to the set they came
	// from, so a snapshot names them from here.
	charsets   [4]CharSet
	charsetIDs [4]byte

	// log is the logger to use.
	logger Logger

	// terminal default colors.
	defaultFg, defaultBg, defaultCur color.Color
	fgColor, bgColor, curColor       color.Color

	// Terminal modes. Written only by the PTY reader goroutine (via setMode,
	// RestoreModes, resetModes) but read from the input/render goroutine
	// (isModeSet). modesMu serializes those cross-goroutine accesses.
	modes   ansi.Modes
	modesMu sync.RWMutex

	// Thread-safe cached mouse mode flags (updated on mode set/reset)
	cachedHasMouse  atomic.Bool
	cachedAllMotion atomic.Bool
	// Thread-safe cached synchronized-output flag (DEC 2026, updated on set/reset)
	cachedSyncOutput atomic.Bool
	// Thread-safe cached auto-wrap flag (DECAWM ?7, updated on set/reset).
	//
	// handleGrapheme consults auto-wrap once per printed character, and reading
	// it out of the modes map cost an RWMutex round trip plus a lookup keyed by
	// an interface, which profiled at 8% of the whole process during a `cat` of
	// a large file: more than the cell write it guards. The map stays
	// authoritative; this is a read-side shortcut for the one mode the hot loop
	// asks about every character.
	cachedAutoWrap atomic.Bool
	// Unix-nanos timestamp of the last sync begin, for the present-anyway timeout
	syncSetAtNanos atomic.Int64
	// Thread-safe cached kitty keyboard flags (updated on push/pop/set/reset)
	cachedKittyFlags atomic.Int32

	// The last written character.
	lastChar rune // either ansi.Rune or ansi.Grapheme
	// A slice of runes to compose a grapheme.
	grapheme []rune
	// The cell handleGrapheme last drew into. A pending wrap makes the target
	// differ from the cursor position observed beforehand, so it is recorded
	// rather than recomputed.
	lastCellX, lastCellY int
	// The cluster left open across a Write boundary, if any.
	openGrapheme openGrapheme

	// The ANSI parser to use.
	parser *ansi.Parser
	// The last parser state.
	lastState parser.State

	cb Callbacks

	// The terminal's icon name and title.
	iconName, title string
	// The current reported working directory. This is not validated.
	cwd string

	// tabstop is the list of tab stops.
	tabstops *uv.TabStops

	// Response pipe: the emulator writes query responses here from inside Write
	// (under the window IO lock), so writes must never block. bufPipe buffers.
	pipe *bufPipe

	// The GL and GR character set identifiers.
	gl, gr  int
	gsingle int // temporarily select GL or GR

	// Indicates if the terminal is closed (atomic for thread-safety).
	closed atomic.Bool

	// atPhantom indicates if the cursor is out of bounds.
	// When true, and a character is written, the cursor is moved to the next line.
	atPhantom bool

	// Cell size in pixels for size reporting (XTWINOPS)
	cellWidth  int
	cellHeight int

	// Kitty graphics state for main and alt screens
	kittyMain *KittyState
	kittyAlt  *KittyState

	// Kitty graphics passthrough callback
	kittyPassthroughFunc func(cmd *KittyCommand, rawData []byte)

	// Sixel graphics state for main and alt screens
	sixelMain *SixelState
	sixelAlt  *SixelState

	// Sixel graphics passthrough callback
	sixelPassthroughFunc func(cmd *SixelCommand, cursorX, cursorY, absLine int)

	// Text sizing (OSC 66) passthrough callback
	textSizingFunc func(rawOSC []byte, cursorX, cursorY, scale, textLen int)

	// Kitty keyboard protocol state
	kittyKbd *kittyKeyboardState

	// semanticMarkers tracks OSC 133 shell integration markers
	semanticMarkers *SemanticMarkerList
}

// NewEmulator creates a new virtual terminal emulator.
func NewEmulator(w, h int) *Emulator {
	t := new(Emulator)
	t.scrs[0] = *NewScreen(w, h)
	t.scrs[1] = *NewScreen(w, h)
	// The alternate screen keeps no scrollback, which every accessor on this
	// type already assumes: Scrollback, ScrollbackLen, ScrollbackLine,
	// ClearScrollback and SetScrollbackMaxLines all read scrs[0]. Its ring was
	// still being filled by every scroll a full-screen application made, at a
	// terminal width of cells per line and 112 bytes per cell, up to the
	// default 10000 lines that SetScrollbackMaxLines never reached because that
	// only resizes the main screen's. Nothing could read a line of it.
	t.scrs[1].DisableScrollback()
	t.scr = &t.scrs[0]
	t.scrs[0].cb = &t.cb
	t.scrs[1].cb = &t.cb
	t.parser = ansi.NewParser()
	t.parser.SetParamsSize(parser.MaxParamsSize)
	t.parser.SetDataSize(1024 * 1024 * 4) // 4MB data buffer
	t.parser.SetHandler(ansi.Handler{
		Print:     t.handlePrint,
		Execute:   t.handleControl,
		HandleCsi: t.handleCsi,
		HandleEsc: t.handleEsc,
		HandleDcs: t.handleDcs,
		HandleOsc: t.handleOsc,
		HandleApc: t.handleApc,
		HandlePm:  t.handlePm,
		HandleSos: t.handleSos,
	})
	t.pipe = newBufPipe()
	t.resetModes()
	t.charsetIDs = defaultCharsetIDs
	t.tabstops = uv.DefaultTabStops(w)

	// Initialize handler maps upfront to avoid nil checks during registration
	t.ccHandlers = make(map[byte][]CcHandler)
	t.dcsHandlers = make(map[int][]DcsHandler)
	t.csiHandlers = make(map[int][]CsiHandler)
	t.oscHandlers = make(map[int][]OscHandler)
	t.escHandler = make(map[int][]EscHandler)

	t.registerDefaultHandlers()

	// Default colors (prevents nil color panics)
	t.defaultFg = color.White
	t.defaultBg = color.Black
	t.defaultCur = color.White

	t.kittyMain = NewKittyState()
	t.kittyAlt = NewKittyState()
	t.registerKittyGraphicsHandler()

	t.sixelMain = NewSixelState()
	t.sixelAlt = NewSixelState()
	t.registerSixelGraphicsHandler()

	t.kittyKbd = newKittyKeyboardState()
	t.registerKittyKeyboardHandlers()

	t.semanticMarkers = NewSemanticMarkerList(10000)

	// Wire scrollback trim to semantic markers adjustment
	if sb := t.scrs[0].Scrollback(); sb != nil {
		sb.SetOnTrim(func(n int) {
			t.semanticMarkers.AdjustForScrollbackTrim(n)
		})
	}

	return t
}

// SetLogger sets the terminal's logger.
func (e *Emulator) SetLogger(l Logger) {
	e.logger = l
}

// SetCallbacks sets the terminal's callbacks.
func (e *Emulator) SetCallbacks(cb Callbacks) {
	e.cb = cb
	e.scrs[0].cb = &e.cb
	e.scrs[1].cb = &e.cb
}

// GetCallbacks returns the terminal's current callbacks.
func (e *Emulator) GetCallbacks() Callbacks {
	return e.cb
}

// SetScreenClearFunc sets the ScreenClear callback without replacing other callbacks.
func (e *Emulator) SetScreenClearFunc(f func()) {
	e.cb.ScreenClear = f
}

// Touched returns the touched lines in the current screen buffer.
func (e *Emulator) Touched() []*uv.LineData {
	return e.scr.Touched()
}

// String returns a string representation of the underlying screen buffer.
func (e *Emulator) String() string {
	s := e.scr.buf.String()
	return uv.TrimSpace(s)
}

// Render renders a snapshot of the terminal screen as a string with styles and
// links encoded as ANSI escape codes.
func (e *Emulator) Render() string {
	return e.scr.buf.Render()
}

var _ uv.Screen = (*Emulator)(nil)

// Bounds returns the bounds of the terminal.
func (e *Emulator) Bounds() uv.Rectangle {
	return e.scr.Bounds()
}

// CellAt returns the current focused screen cell at the given x, y position.
// It returns nil if the cell is out of bounds.
func (e *Emulator) CellAt(x, y int) *uv.Cell {
	return e.scr.CellAt(x, y)
}

// SetCell sets the current focused screen cell at the given x, y position.
func (e *Emulator) SetCell(x, y int, c *uv.Cell) {
	e.scr.SetCell(x, y, c)
}

// MainCellAt reads a cell from the normal screen whether or not the alternate
// one is active. It is what the guest is not looking at while a full-screen
// program is running, and what quitting that program reveals.
func (e *Emulator) MainCellAt(x, y int) *uv.Cell {
	return e.scrs[0].CellAt(x, y)
}

// SetMainCell writes a cell into the normal screen whether or not the alternate
// one is active.
func (e *Emulator) SetMainCell(x, y int, c *uv.Cell) {
	e.scrs[0].SetCell(x, y, c)
}

// Scrollback returns the scrollback buffer of the main screen.
// Note: The alternate screen does not maintain scrollback.
func (e *Emulator) Scrollback() *Scrollback {
	return e.scrs[0].Scrollback()
}

// ClearScrollback clears the scrollback buffer of the main screen.
func (e *Emulator) ClearScrollback() {
	e.scrs[0].ClearScrollback()
}

// ScrollbackLen returns the number of lines in the scrollback buffer.
func (e *Emulator) ScrollbackLen() int {
	return e.scrs[0].ScrollbackLen()
}

// SemanticMarkers returns the list of OSC 133 semantic zone markers.
func (e *Emulator) SemanticMarkers() *SemanticMarkerList {
	return e.semanticMarkers
}

// extractCommandText extracts the command text between a B marker position
// and a C marker position. Called at C-marker time before output overwrites the buffer.
func (e *Emulator) extractCommandText(bLine, bCol, cLine, _ int) string {
	sbLen := e.ScrollbackLen()
	width := e.Width()
	height := e.Height()

	readLine := func(absLine int) string {
		if absLine < sbLen {
			line := e.ScrollbackLine(absLine)
			if line == nil {
				return ""
			}
			var sb strings.Builder
			for _, cell := range line {
				if cell.Content != "" {
					sb.WriteString(string(cell.Content))
				} else {
					sb.WriteByte(' ')
				}
			}
			return strings.TrimRight(sb.String(), " ")
		}
		screenY := absLine - sbLen
		if screenY < 0 || screenY >= height {
			return ""
		}
		var sb strings.Builder
		for x := range width {
			cell := e.CellAt(x, screenY)
			if cell != nil && cell.Content != "" {
				sb.WriteString(string(cell.Content))
			} else {
				sb.WriteByte(' ')
			}
		}
		return strings.TrimRight(sb.String(), " ")
	}

	// Single-line command (most common case)
	if bLine == cLine || cLine == bLine+1 {
		full := readLine(bLine)
		runes := []rune(full)
		if bCol >= len(runes) {
			return ""
		}
		return strings.TrimSpace(string(runes[bCol:]))
	}

	// Multi-line command
	var parts []string
	firstLine := readLine(bLine)
	runes := []rune(firstLine)
	if bCol < len(runes) {
		parts = append(parts, strings.TrimSpace(string(runes[bCol:])))
	}
	for line := bLine + 1; line < cLine; line++ {
		parts = append(parts, readLine(line))
	}
	return strings.Join(parts, "\n")
}

// ScrollbackLine returns a line from the scrollback buffer at the given index.
// Index 0 is the oldest line. Returns nil if index is out of bounds.
func (e *Emulator) ScrollbackLine(index int) uv.Line {
	return e.scrs[0].ScrollbackLine(index)
}

// SetScrollbackMaxLines sets the maximum number of lines for the scrollback buffer.
func (e *Emulator) SetScrollbackMaxLines(maxLines int) {
	e.scrs[0].SetScrollbackMaxLines(maxLines)
}

// WidthMethod returns the width method used by the terminal.
func (e *Emulator) WidthMethod() uv.WidthMethod {
	if e.isModeSet(ansi.ModeUnicodeCore) {
		return ansi.GraphemeWidth
	}
	return ansi.WcWidth
}

// Draw implements the [uv.Drawable] interface.
func (e *Emulator) Draw(scr uv.Screen, area uv.Rectangle) {
	bg := uv.EmptyCell
	bg.Style.Bg = e.bgColor
	screen.FillArea(scr, &bg, area)
	for y := range e.Touched() {
		if y < 0 || y >= e.Height() {
			continue
		}
		for x := 0; x < e.Width(); {
			w := 1
			cell := e.CellAt(x, y)
			if cell != nil {
				cell = cell.Clone()
				if cell.Width > 1 {
					w = cell.Width
				}
				if cell.Style.Bg == nil && e.bgColor != nil {
					cell.Style.Bg = e.bgColor
				}
				if cell.Style.Fg == nil && e.fgColor != nil {
					cell.Style.Fg = e.fgColor
				}
				scr.SetCell(x+area.Min.X, y+area.Min.Y, cell)
			}
			x += w
		}
	}
}

// Height returns the height of the terminal.
func (e *Emulator) Height() int {
	return e.scr.Height()
}

// Width returns the width of the terminal.
func (e *Emulator) Width() int {
	return e.scr.Width()
}

// SetCellSize sets the pixel dimensions of a single character cell.
// Used for XTWINOPS terminal size reporting.
func (e *Emulator) SetCellSize(width, height int) {
	e.cellWidth = width
	e.cellHeight = height
}

// CellSize returns the pixel dimensions of a single character cell.
func (e *Emulator) CellSize() (width, height int) {
	// Default to 8x16 pixels if not set (common VGA text mode dimensions)
	if e.cellWidth == 0 || e.cellHeight == 0 {
		return 8, 16
	}
	return e.cellWidth, e.cellHeight
}

// CursorPosition returns the terminal's cursor position.
func (e *Emulator) CursorPosition() uv.Position {
	x, y := e.scr.CursorPosition()
	return uv.Pos(x, y)
}

// ReserveImageSpace reserves space for an image by moving cursor and outputting placeholders.
// This ensures subsequent output appears below the image rather than on top of it.
func (e *Emulator) ReserveImageSpace(rows, cols int) {
	if rows <= 0 {
		return
	}
	_, startY := e.scr.CursorPosition()
	height := e.scr.Height()

	// Calculate how many scrolls are needed
	endY := startY + rows
	scrollCount := 0
	if endY > height {
		// clamp: rows beyond the viewport cannot be shown, and a hostile r=
		// could otherwise drive ~1e9 ScrollUp calls while holding the IO lock.
		scrollCount = min(endY-height, height)
		for range scrollCount {
			e.scr.ScrollUp(1)
		}
	}

	// Final cursor position accounts for scrolling
	// After scrolling, the original startY has moved up by scrollCount
	finalY := startY + rows - scrollCount
	if finalY >= height {
		finalY = height - 1
	}
	e.scr.setCursor(0, finalY, false)
}

// IsCursorHidden returns whether the cursor is currently hidden.
// Applications can hide the cursor using ANSI escape sequences (DECTCEM mode).
func (e *Emulator) IsCursorHidden() bool {
	return e.scr.Cursor().Hidden
}

// IsAltScreen returns whether the terminal is currently using the alternate screen buffer.
// The alternate screen is used by full-screen applications like vim, less, htop, btop, etc.
// This is important for mouse event forwarding - mouse events should only be forwarded
// to applications when they are in alternate screen mode.
func (e *Emulator) IsAltScreen() bool {
	return e.isModeSet(ansi.ModeAltScreen) || e.isModeSet(ansi.ModeAltScreenSaveCursor)
}

// ActiveScreenIsAlt reports whether the active screen pointer currently
// addresses the alternate buffer. This is a diagnostic accessor: it exists so
// the render trace can distinguish the buffer actually being read from the mode
// bits reported by IsAltScreen, which RestoreAltScreenMode deliberately leaves
// untouched. It is not part of the emulator's behavioural contract, so do not
// build rendering or input logic on it.
func (e *Emulator) ActiveScreenIsAlt() bool {
	return e.scr == &e.scrs[1]
}

// RestoreAltScreenMode restores the alternate screen mode state.
// This is used when reconnecting to a daemon session to restore the emulator state
// without re-sending the escape sequences that would trigger the mode change.
// This method ONLY switches the screen buffer pointer - it does NOT modify the
// modes map to avoid concurrent map access issues.
func (e *Emulator) RestoreAltScreenMode(enabled bool) {
	if enabled {
		// Switch to alt screen buffer if not already there
		// Don't clear it - we want to preserve any content that gets restored
		if e.scr != &e.scrs[1] {
			e.scr = &e.scrs[1]
		}
	} else {
		// Switch to main screen buffer if not already there
		if e.scr != &e.scrs[0] {
			e.scr = &e.scrs[0]
		}
	}
	// NOTE: We don't modify e.modes[] here to avoid concurrent map access.
	// The modes will be updated naturally when PTY output is processed.
}

// RestoreCursorPosition puts the cursor back where a restored snapshot had it.
// It is the counterpart of CursorPosition and, like RestoreAltScreenMode, it
// exists so reconnecting does not have to re-send escape sequences whose side
// effects would undo the restore.
func (e *Emulator) RestoreCursorPosition(x, y int) {
	e.setCursor(x, y)
}

// defaultCharsetIDs is US ASCII in all four slots, which is what an emulator
// that has been sent no SCS sequence is using.
var defaultCharsetIDs = [4]byte{'B', 'B', 'B', 'B'}

// ScrollRegion returns the margins scrolling is confined to, as the rectangle
// of the active screen they cover.
func (e *Emulator) ScrollRegion() uv.Rectangle {
	return e.scr.ScrollRegion()
}

// RestoreScrollRegion puts back the margins a guest set with DECSTBM or DECSLRM.
// A guest sets them once to hold a header or a status line out of the scrolling
// part of the screen, so a client that comes back without them scrolls the whole
// screen and takes the fixed rows with it.
func (e *Emulator) RestoreScrollRegion(r uv.Rectangle) {
	if r.Empty() {
		return
	}
	e.scr.scroll = r.Intersect(e.scr.Bounds())
}

// ResetScrollRegion puts scrolling back to the whole screen, which is where a
// pane whose guest has set no margins scrolls.
func (e *Emulator) ResetScrollRegion() {
	e.scr.scroll = e.scr.Bounds()
}

// Charsets returns the designator byte of the character set selected into each
// of G0 to G3, and which of them GL and GR are pointing at.
func (e *Emulator) Charsets() (ids [4]byte, gl, gr int) {
	return e.charsetIDs, e.gl, e.gr
}

// RestoreCharsets puts back a character set selection. A program that draws
// boxes selects the DEC line-drawing set once and then sends the box characters
// as plain letters, so a client that comes back with G0 at US ASCII draws qqqq
// where the guest drew a horizontal rule.
func (e *Emulator) RestoreCharsets(ids [4]byte, gl, gr int) {
	for i, id := range ids {
		switch id {
		case 'A':
			e.charsets[i] = UK
		case '0':
			e.charsets[i] = SpecialDrawing
		default:
			e.charsets[i] = nil
			id = 'B'
		}
		e.charsetIDs[i] = id
	}
	if gl >= 0 && gl < 4 {
		e.gl = gl
	}
	if gr >= 0 && gr < 4 {
		e.gr = gr
	}
}

// CursorPen returns the graphic rendition in force: the style and hyperlink
// everything written next will be painted with. A guest sets it once with an
// SGR sequence and every character until the next one inherits it, so it is
// state a snapshot has to carry and not something the cells can be read back
// from.
func (e *Emulator) CursorPen() (uv.Style, uv.Link) {
	return e.scr.cursorPen(), e.scr.cursorLink()
}

// RestoreCursorPen puts back the rendition a snapshot was taken under, so the
// output that arrives after the snapshot is painted the colour the guest set
// rather than whatever this emulator was left in.
func (e *Emulator) RestoreCursorPen(pen uv.Style, link uv.Link) {
	e.scr.cur.Pen = pen
	e.scr.cur.Link = link
}

// GetModes returns a copy of the current terminal DEC private modes.
// This is used for session state serialization to preserve terminal modes
// across reconnections (mouse tracking, bracketed paste, etc.).
//
// It captures every DEC mode the emulator tracks rather than a hand-picked
// list: a guest sets a sticky mode once at startup (a browser enables
// 1003/1006/1016 and never repeats them), and any mode missing here is
// silently lost on reattach once the enable sequence has scrolled out of the
// daemon's bounded output buffer.
func (e *Emulator) GetModes() map[int]bool {
	modes := make(map[int]bool)

	e.modesMu.RLock()
	for mode, setting := range e.modes {
		dec, ok := mode.(ansi.DECMode)
		if !ok {
			// ANSI modes share the int keyspace with DEC modes in this
			// serialization, so they cannot be restored unambiguously.
			continue
		}
		if dec == ansi.ModeSynchronizedOutput {
			// Transient frame gate: restoring it would hold the first frame
			// after attach until the sync timeout expires.
			continue
		}
		switch {
		case setting.IsSet():
			modes[int(dec)] = true
		case setting.IsReset():
			modes[int(dec)] = false
		}
	}
	e.modesMu.RUnlock()

	return modes
}

// RestoreModes restores terminal modes from a saved state.
// This is used when reconnecting to a daemon session to restore mouse tracking
// and other terminal modes without triggering mode change side effects.
func (e *Emulator) RestoreModes(modes map[int]bool) {
	if modes == nil {
		return
	}

	// Restore each mode by directly updating the modes map
	// This avoids triggering side effects like screen clearing
	e.modesMu.Lock()
	for modeNum, enabled := range modes {
		// Convert int back to Mode
		mode := ansi.DECMode(modeNum)

		if enabled {
			e.modes[mode] = ansi.ModeSet
		} else {
			e.modes[mode] = ansi.ModeReset
		}
		// This is the one write path that bypasses setMode, so the read-side
		// caches it maintains have to be refreshed here or they go stale.
		if mode == ansi.ModeAutoWrap {
			e.cachedAutoWrap.Store(enabled)
		}
	}
	e.modesMu.Unlock()

	// Refresh the atomic mouse-mode caches that HasMouseMode and
	// HasAllMotionMode read. Leaving them stale broke mouse routing on every
	// daemon reattach: the modes map said 1003 was set, but the input layer
	// consults the cache, saw false, and sent wheel/motion/click to
	// scrollback and copy mode instead of the pane. Must run after the map
	// lock is released; it re-reads the map through isModeSet.
	e.updateMouseModeCache()

	// setMode's cursor-visibility side effect, for the same reason: a guest
	// that hid its cursor (DECTCEM reset) must not get it back on reattach.
	if enabled, ok := modes[int(ansi.ModeTextCursorEnable)]; ok {
		e.scr.setCursorHidden(!enabled)
	}
}

// HasMouseMode returns true if any mouse tracking mode is enabled.
// HasMouseMode returns true if any mouse tracking mode is enabled.
// Thread-safe: reads from an atomic cache updated on mode set/reset.
func (e *Emulator) HasMouseMode() bool {
	return e.cachedHasMouse.Load()
}

// HasAllMotionMode returns true only if the child app requested mode 1003.
// Thread-safe: reads from an atomic cache updated on mode set/reset.
func (e *Emulator) HasAllMotionMode() bool {
	return e.cachedAllMotion.Load()
}

// syncMaxHold bounds how long a synchronized update is honored. An app that
// opens sync and never closes it (a crash, or a screen switch mid-frame) must
// not freeze the window; real terminals present anyway after a short timeout.
const syncMaxHold = 150 * time.Millisecond

// IsSyncActive reports whether the guest has an open synchronized update
// (DEC private mode 2026): it has begun drawing a frame and does not want it
// presented until it resets the mode. Thread-safe: reads from atomics updated on
// mode set/reset. Returns false once the update has been open past syncMaxHold.
func (e *Emulator) IsSyncActive() bool {
	if !e.cachedSyncOutput.Load() {
		return false
	}
	return time.Now().UnixNano()-e.syncSetAtNanos.Load() < int64(syncMaxHold)
}

// updateMouseModeCache recalculates the cached mouse mode flags.
// Must be called from the VT processing goroutine after mode changes.
func (e *Emulator) updateMouseModeCache() {
	hasMouse := false
	for _, m := range []ansi.DECMode{
		ansi.ModeMouseX10,
		ansi.ModeMouseNormal,
		ansi.ModeMouseHighlight,
		ansi.ModeMouseButtonEvent,
		ansi.ModeMouseAnyEvent,
	} {
		if e.isModeSet(m) {
			hasMouse = true
			break
		}
	}
	e.cachedHasMouse.Store(hasMouse)
	e.cachedAllMotion.Store(e.isModeSet(ansi.ModeMouseAnyEvent))
}

// HasCellMotionMode returns true if the child app requested mode 1002
// (button-event tracking), which reports motion while a button is pressed.
func (e *Emulator) HasCellMotionMode() bool {
	return e.isModeSet(ansi.ModeMouseButtonEvent)
}

// SupportsMotionEvents returns true if the child app's mouse mode supports
// motion events (modes 1002 or 1003). Modes 1000/1001 only support click/release.
func (e *Emulator) SupportsMotionEvents() bool {
	return e.isModeSet(ansi.ModeMouseButtonEvent) || e.isModeSet(ansi.ModeMouseAnyEvent)
}

// EncodeMouseEvent encodes a mouse event as an escape sequence string.
// Returns empty string if no mouse mode is enabled.
// This is used for daemon mode where mouse events need to be sent through the PTY.
func (e *Emulator) EncodeMouseEvent(m Mouse) string {
	var (
		enc  ansi.Mode
		mode ansi.Mode
	)

	for _, mm := range []ansi.DECMode{
		ansi.ModeMouseX10,
		ansi.ModeMouseNormal,
		ansi.ModeMouseHighlight,
		ansi.ModeMouseButtonEvent,
		ansi.ModeMouseAnyEvent,
	} {
		if e.isModeSet(mm) {
			mode = mm
		}
	}

	if mode == nil {
		return ""
	}

	for _, mm := range []ansi.DECMode{
		ansi.ModeMouseExtSgr,
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

	return e.encodeMouseReport(enc, b, mouse.X, mouse.Y, isRelease)
}

// Resize resizes the terminal.
func (e *Emulator) Resize(width int, height int) {
	// Guard against 0 or negative terminal dimensions (e.g., laptop lid close or display disconnect)
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}

	// A resize reflows and reclamps, so the cell an open cluster was drawn into
	// no longer identifies that cluster. Close it.
	e.openGrapheme = openGrapheme{}

	x, y := e.scr.CursorPosition()
	oldHeight := e.Height()

	if e.atPhantom {
		if x < width-1 {
			e.atPhantom = false
			x++
		}
	}

	if y < 0 {
		y = 0
	}

	// Auto-scroll to keep cursor visible when height is reduced.
	// This prevents the prompt from going off-screen below the viewport.
	if y >= height && oldHeight > height {
		linesToScroll := y - (height - 1)
		// Scroll content up (pushes lines to scrollback)
		e.scr.ScrollUp(linesToScroll)
		// Cursor moves to bottom of new viewport
		y = height - 1
	} else if y >= height {
		y = height - 1
	}

	if x < 0 {
		x = 0
	}
	if x >= width {
		x = width - 1
	}

	// Trigger scrollback reflow when width changes to handle soft-wrapping
	if width != e.Width() && e.Scrollback() != nil {
		e.Scrollback().Reflow(width)
	}

	e.scrs[0].Resize(width, height)
	e.scrs[1].Resize(width, height)
	e.tabstops = uv.DefaultTabStops(width)

	e.setCursor(x, y)

	if e.isModeSet(ansi.ModeInBandResize) {
		_, _ = io.WriteString(e.pipe, ansi.InBandResize(e.Height(), e.Width(), 0, 0))
	}
}

// Read reads data from the terminal input buffer.
func (e *Emulator) Read(p []byte) (n int, err error) {
	if e.closed.Load() {
		return 0, io.EOF
	}

	return e.pipe.Read(p) //nolint:wrapcheck
}

// Close closes the terminal.
func (e *Emulator) Close() error {
	if e.closed.Load() {
		return nil
	}

	e.closed.Store(true)
	// Close the response pipe so a reader blocked in Read (the terminal-response
	// forwarder) unblocks with EOF instead of leaking.
	if e.pipe != nil {
		_ = e.pipe.Close()
	}
	return nil
}

// Write writes data to the terminal output buffer.
func (e *Emulator) Write(p []byte) (n int, err error) {
	if e.closed.Load() {
		return 0, io.ErrClosedPipe
	}

	for i := range p {
		e.parser.Advance(p[i])
		state := e.parser.State()
		// flush grapheme if we transitioned to a non-utf8 state or we have
		// written the whole byte slice.
		if len(e.grapheme) > 0 {
			if e.lastState == parser.GroundState && state != parser.Utf8State {
				e.flushGrapheme()
			} else if i == len(p)-1 {
				// Out of bytes, possibly mid-cluster: draw what we have but
				// keep the trailing cluster open for the next Write.
				e.flushGraphemeAtWriteEnd()
			}
		}
		e.lastState = state
	}
	return len(p), nil
}

// WriteString writes a string to the terminal output buffer.
func (e *Emulator) WriteString(s string) (n int, err error) {
	return e.Write([]byte(s)) //nolint:wrapcheck
}

// InputPipe returns the terminal's input pipe.
// This can be used to send input to the terminal.
func (e *Emulator) InputPipe() io.Writer {
	return e.pipe
}

// Paste pastes text into the terminal.
// If bracketed paste mode is enabled, the text is bracketed with the
// appropriate escape sequences.
func (e *Emulator) Paste(text string) {
	if e.isModeSet(ansi.ModeBracketedPaste) {
		_, _ = io.WriteString(e.pipe, ansi.BracketedPasteStart)
		defer io.WriteString(e.pipe, ansi.BracketedPasteEnd) //nolint:errcheck
	}

	_, _ = io.WriteString(e.pipe, text)
}

// SendText sends arbitrary text to the terminal.
func (e *Emulator) SendText(text string) {
	_, _ = io.WriteString(e.pipe, text)
}

// SendKeys sends multiple keys to the terminal.
func (e *Emulator) SendKeys(keys ...uv.KeyEvent) {
	for _, k := range keys {
		e.SendKey(k)
	}
}

// ForegroundColor returns the terminal's foreground color. This returns nil if
// the foreground color is not set which means the outer terminal color is
// used.
func (e *Emulator) ForegroundColor() color.Color {
	if e.fgColor != nil {
		return e.fgColor
	}
	if e.defaultFg != nil {
		return e.defaultFg
	}
	return color.White // ultimate fallback
}

// SetForegroundColor sets the terminal's foreground color.
func (e *Emulator) SetForegroundColor(c color.Color) {
	if c == nil {
		c = e.defaultFg
	}
	e.fgColor = c
	if e.cb.ForegroundColor != nil {
		e.cb.ForegroundColor(c)
	}
}

// SetDefaultForegroundColor sets the terminal's default foreground color.
func (e *Emulator) SetDefaultForegroundColor(c color.Color) {
	e.defaultFg = c
}

// BackgroundColor returns the terminal's background color. This returns nil if
// the background color is not set which means the outer terminal color is
// used.
func (e *Emulator) BackgroundColor() color.Color {
	if e.bgColor != nil {
		return e.bgColor
	}
	if e.defaultBg != nil {
		return e.defaultBg
	}
	return color.Black // ultimate fallback
}

// SetBackgroundColor sets the terminal's background color.
func (e *Emulator) SetBackgroundColor(c color.Color) {
	if c == nil {
		c = e.defaultBg
	}
	e.bgColor = c
	if e.cb.BackgroundColor != nil {
		e.cb.BackgroundColor(c)
	}
}

// SetDefaultBackgroundColor sets the terminal's default background color.
func (e *Emulator) SetDefaultBackgroundColor(c color.Color) {
	e.defaultBg = c
}

// CursorColor returns the terminal's cursor color. This returns nil if the
// cursor color is not set which means the outer terminal color is used.
func (e *Emulator) CursorColor() color.Color {
	if e.curColor == nil {
		return e.defaultCur
	}
	return e.curColor
}

// SetCursorColor sets the terminal's cursor color.
func (e *Emulator) SetCursorColor(c color.Color) {
	if c == nil {
		c = e.defaultCur
	}
	e.curColor = c
	if e.cb.CursorColor != nil {
		e.cb.CursorColor(c)
	}
}

// SetDefaultCursorColor sets the terminal's default cursor color.
func (e *Emulator) SetDefaultCursorColor(c color.Color) {
	if c == nil {
		c = color.White
	}
	e.defaultCur = c
}

// IndexedColor returns a terminal's indexed color. An indexed color is a color
// between 0 and 255.
func (e *Emulator) IndexedColor(i int) color.Color {
	if i < 0 || i > 255 {
		return nil
	}

	c := e.colors[i]
	if c == nil {
		// Return the default color. Safe conversion: i is already validated to be in [0, 255]
		// #nosec G115 - false positive, i is validated to be in valid uint8 range above
		return ansi.IndexedColor(uint8(i))
	}

	return c
}

// PaletteColor resolves one of the sixteen ANSI palette slots the way handleSgr
// resolves SGR 30-37 and 90-97: through the user's theme when one is set, and
// as a plain palette entry otherwise.
//
// A cell rebuilt from a snapshot has to be coloured by the same rule as a cell
// the guest writes live, or a pane comes back in one palette and carries on in
// another.
func (e *Emulator) PaletteColor(i int) color.Color {
	if i < 0 || i > 15 {
		return nil
	}
	if !e.hasThemeColors() {
		// #nosec G115 - i is validated to be in [0, 15] above
		return ansi.BasicColor(uint8(i))
	}
	return e.IndexedColor(i)
}

// SetIndexedColor sets a terminal's indexed color.
// The index must be between 0 and 255.
func (e *Emulator) SetIndexedColor(i int, c color.Color) {
	if i < 0 || i > 255 {
		return
	}

	e.colors[i] = c
}

// SetThemeColors sets the terminal's color palette from a theme.
// This sets the default foreground, background, cursor colors and the
// first 16 ANSI colors (0-15) which are used by terminal applications.
// If fg, bg, and cur are all nil, theming is disabled and only default colors are set.
func (e *Emulator) SetThemeColors(fg, bg, cur color.Color, ansiPalette [16]color.Color) {
	e.SetDefaultForegroundColor(fg)
	e.SetDefaultBackgroundColor(bg)
	e.SetDefaultCursorColor(cur)

	// Only set indexed colors if we have a theme (fg/bg are not nil)
	// This prevents overriding standard terminal colors when theming is disabled
	if fg != nil || bg != nil {
		// Set the first 16 ANSI colors
		for i := range 16 {
			e.SetIndexedColor(i, ansiPalette[i])
		}
	}
}

// hasThemeColors returns true if theme colors have been set
func (e *Emulator) hasThemeColors() bool {
	// Check if any indexed colors have been set
	// If colors[0] is nil, no theme has been applied
	return e.colors[0] != nil
}

// resetTabStops resets the terminal tab stops to the default set.
func (e *Emulator) resetTabStops() {
	e.tabstops = uv.DefaultTabStops(e.Width())
}

func (e *Emulator) logf(format string, v ...any) {
	if e.logger != nil {
		e.logger.Printf(format, v...)
	}
}

// WriteResponse writes data to the emulator's response pipe.
// This allows external code (e.g., daemon-side Kitty query handlers)
// to inject responses that will be forwarded to the PTY.
func (e *Emulator) WriteResponse(data []byte) {
	_, _ = e.pipe.Write(data)
}

func (e *Emulator) registerKittyGraphicsHandler() {
	e.RegisterApcHandler(func(data []byte) bool {
		if len(data) < 1 || data[0] != 'G' {
			return false
		}

		cmd, err := ParseKittyCommand(data[1:])
		if err != nil || cmd == nil {
			return false
		}
		if !cmd.More {
			e.logf("KITTY APC: m=0 chunk received, action=%c", cmd.Action)
		}

		// Build complete APC sequence: ESC _ G<params>;<payload> ESC \
		// APC terminator is ESC \ (0x1b 0x5c), not just \
		rawData := make([]byte, len(data)+4)
		rawData[0] = '\x1b'
		rawData[1] = '_'
		copy(rawData[2:], data)
		rawData[len(rawData)-2] = '\x1b'
		rawData[len(rawData)-1] = '\\'

		if e.kittyPassthroughFunc != nil {
			e.kittyPassthroughFunc(cmd, rawData)
			return true
		}

		state := e.kittyMain
		if e.IsAltScreen() {
			state = e.kittyAlt
		}

		handler := NewKittyGraphicsHandler(e.scr, state, e.pipe)
		return handler.HandleCommand(cmd)
	})
}

func (e *Emulator) SetKittyPassthroughFunc(fn func(cmd *KittyCommand, rawData []byte)) {
	e.kittyPassthroughFunc = fn
}

func (e *Emulator) KittyState() *KittyState {
	if e.IsAltScreen() {
		return e.kittyAlt
	}
	return e.kittyMain
}

func (e *Emulator) KittyMainState() *KittyState {
	return e.kittyMain
}

func (e *Emulator) KittyAltState() *KittyState {
	return e.kittyAlt
}

func (e *Emulator) registerSixelGraphicsHandler() {
	// Sixel DCS format: ESC P <p1>;<p2>;<p3> q <sixel-data> ST
	// The DCS command byte is 'q' (the sixel introducer)
	// The ansi library uses Command(0, 0, 'q') for simple DCS commands
	e.RegisterDcsHandler(int('q'), func(params ansi.Params, data []byte) bool {
		// Reconstruct the full DCS data (params + 'q' + data)
		// The params have already been parsed by the ansi library
		var fullData []byte

		// Build parameter string
		for i, p := range params {
			if i > 0 {
				fullData = append(fullData, ';')
			}
			val := p.Param(0)
			// Convert int to string bytes
			if val == 0 {
				fullData = append(fullData, '0')
			} else {
				digits := make([]byte, 0, 10)
				for val > 0 {
					digits = append(digits, byte('0'+val%10))
					val /= 10
				}
				// Reverse digits
				for i := len(digits) - 1; i >= 0; i-- {
					fullData = append(fullData, digits[i])
				}
			}
		}

		// Add 'q' introducer and data
		fullData = append(fullData, 'q')
		fullData = append(fullData, data...)

		cmd := ParseSixelCommand(fullData)
		if cmd == nil {
			return false
		}

		// Get cursor position for placement
		cursorX, cursorY := e.scr.CursorPosition()

		// Calculate absolute line (accounting for scrollback)
		absLine := e.scrs[0].ScrollbackLen() + cursorY
		if e.IsAltScreen() {
			// Alt screen doesn't have scrollback, use viewport position
			absLine = cursorY
		}

		// If passthrough is enabled, forward to host terminal
		if e.sixelPassthroughFunc != nil {
			e.sixelPassthroughFunc(cmd, cursorX, cursorY, absLine)
			// Reserve space for the image (move cursor down)
			cellWidth, cellHeight := e.CellSize()
			rows := cmd.RowsForHeight(cellHeight)
			cols := cmd.ColsForWidth(cellWidth)
			if rows > 0 {
				e.ReserveImageSpace(rows, cols)
			}
			return true
		}

		// Local handling: store placement in state
		state := e.sixelMain
		if e.IsAltScreen() {
			state = e.sixelAlt
		}

		cellWidth, cellHeight := e.CellSize()
		placement := &SixelPlacement{
			AbsoluteLine:   absLine,
			ScreenX:        cursorX,
			Width:          cmd.Width,
			Height:         cmd.Height,
			Rows:           cmd.RowsForHeight(cellHeight),
			Cols:           cmd.ColsForWidth(cellWidth),
			Data:           cmd.Data,
			RawSequence:    cmd.RawSequence,
			AspectRatio:    cmd.AspectRatio,
			BackgroundMode: cmd.BackgroundMode,
		}

		state.AddPlacement(placement)

		// Reserve space for the image
		if placement.Rows > 0 {
			e.ReserveImageSpace(placement.Rows, placement.Cols)
		}

		return true
	})
}

func (e *Emulator) SetSixelPassthroughFunc(fn func(cmd *SixelCommand, cursorX, cursorY, absLine int)) {
	e.sixelPassthroughFunc = fn
}

func (e *Emulator) SetTextSizingFunc(fn func(rawOSC []byte, cursorX, cursorY, scale, textLen int)) {
	e.textSizingFunc = fn
}

func (e *Emulator) SixelState() *SixelState {
	if e.IsAltScreen() {
		return e.sixelAlt
	}
	return e.sixelMain
}
