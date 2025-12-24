package vt

import (
	"image/color"
	"io"

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

	// Character sets
	charsets [4]CharSet

	// log is the logger to use.
	logger Logger

	// terminal default colors.
	defaultFg, defaultBg, defaultCur color.Color
	fgColor, bgColor, curColor       color.Color

	// Terminal modes.
	modes ansi.Modes

	// The last written character.
	lastChar rune // either ansi.Rune or ansi.Grapheme
	// A slice of runes to compose a grapheme.
	grapheme []rune

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

	// I/O pipes.
	pr *io.PipeReader
	pw *io.PipeWriter

	// The GL and GR character set identifiers.
	gl, gr  int
	gsingle int // temporarily select GL or GR

	// Indicates if the terminal is closed.
	closed bool

	// atPhantom indicates if the cursor is out of bounds.
	// When true, and a character is written, the cursor is moved to the next line.
	atPhantom bool

	// Cell size in pixels for size reporting (XTWINOPS)
	cellWidth  int
	cellHeight int
}

// NewEmulator creates a new virtual terminal emulator.
func NewEmulator(w, h int) *Emulator {
	t := new(Emulator)
	t.scrs[0] = *NewScreen(w, h)
	t.scrs[1] = *NewScreen(w, h)
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
	t.pr, t.pw = io.Pipe()
	t.resetModes()
	t.tabstops = uv.DefaultTabStops(w)
	t.registerDefaultHandlers()

	// Default colors (prevents nil color panics)
	t.defaultFg = color.White
	t.defaultBg = color.Black
	t.defaultCur = color.White

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

// ScrollbackLine returns a line from the scrollback buffer at the given index.
// Index 0 is the oldest line. Returns nil if index is out of bounds.
func (e *Emulator) ScrollbackLine(index int) []uv.Cell {
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

// GetModes returns a copy of the current terminal modes.
// This is used for session state serialization to preserve terminal modes
// across reconnections (mouse tracking, bracketed paste, etc.).
func (e *Emulator) GetModes() map[int]bool {
	modes := make(map[int]bool)

	// Important modes to preserve for session restoration:
	modesToCapture := []ansi.Mode{
		// Mouse tracking modes
		ansi.ModeMouseX10,         // ?9
		ansi.ModeMouseNormal,      // ?1000
		ansi.ModeMouseHighlight,   // ?1001
		ansi.ModeMouseButtonEvent, // ?1002
		ansi.ModeMouseAnyEvent,    // ?1003
		ansi.ModeMouseExtSgr,      // ?1006 - SGR mouse encoding

		// Screen and cursor modes
		ansi.ModeAltScreen,           // ?1047
		ansi.ModeAltScreenSaveCursor, // ?1049

		// Other important modes
		ansi.ModeBracketedPaste, // ?2004
		ansi.ModeFocusEvent,     // ?1004
		ansi.ModeAutoWrap,       // ?7
	}

	for _, mode := range modesToCapture {
		if e.isModeSet(mode) {
			// Store mode number as int for JSON serialization
			modes[int(mode.Mode())] = true
		}
	}

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
	for modeNum, enabled := range modes {
		// Convert int back to Mode
		mode := ansi.DECMode(modeNum)

		if enabled {
			e.modes[mode] = ansi.ModeSet
		} else {
			e.modes[mode] = ansi.ModeReset
		}
	}
}

// HasMouseMode returns true if any mouse tracking mode is enabled.
// This is useful for debugging mouse event forwarding issues.
func (e *Emulator) HasMouseMode() bool {
	for _, m := range []ansi.DECMode{
		ansi.ModeMouseX10,
		ansi.ModeMouseNormal,
		ansi.ModeMouseHighlight,
		ansi.ModeMouseButtonEvent,
		ansi.ModeMouseAnyEvent,
	} {
		if e.isModeSet(m) {
			return true
		}
	}
	return false
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

	switch enc {
	case nil: // X10 mouse encoding
		return ansi.MouseX10(b, mouse.X, mouse.Y)
	case ansi.ModeMouseExtSgr: // SGR mouse encoding
		return ansi.MouseSgr(b, mouse.X, mouse.Y, isRelease)
	}
	return ""
}

// Resize resizes the terminal.
func (e *Emulator) Resize(width int, height int) {
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
		_, _ = io.WriteString(e.pw, ansi.InBandResize(e.Height(), e.Width(), 0, 0))
	}
}

// Read reads data from the terminal input buffer.
func (e *Emulator) Read(p []byte) (n int, err error) {
	if e.closed {
		return 0, io.EOF
	}

	return e.pr.Read(p) //nolint:wrapcheck
}

// Close closes the terminal.
func (e *Emulator) Close() error {
	if e.closed {
		return nil
	}

	e.closed = true
	return nil
}

// Write writes data to the terminal output buffer.
func (e *Emulator) Write(p []byte) (n int, err error) {
	if e.closed {
		return 0, io.ErrClosedPipe
	}

	for i := range p {
		e.parser.Advance(p[i])
		state := e.parser.State()
		// flush grapheme if we transitioned to a non-utf8 state or we have
		// written the whole byte slice.
		if len(e.grapheme) > 0 {
			if (e.lastState == parser.GroundState && state != parser.Utf8State) || i == len(p)-1 {
				e.flushGrapheme()
			}
		}
		e.lastState = state
	}
	return len(p), nil
}

// WriteString writes a string to the terminal output buffer.
func (e *Emulator) WriteString(s string) (n int, err error) {
	return io.WriteString(e, s) //nolint:wrapcheck
}

// InputPipe returns the terminal's input pipe.
// This can be used to send input to the terminal.
func (e *Emulator) InputPipe() io.Writer {
	return e.pw
}

// Paste pastes text into the terminal.
// If bracketed paste mode is enabled, the text is bracketed with the
// appropriate escape sequences.
func (e *Emulator) Paste(text string) {
	if e.isModeSet(ansi.ModeBracketedPaste) {
		_, _ = io.WriteString(e.pw, ansi.BracketedPasteStart)
		defer io.WriteString(e.pw, ansi.BracketedPasteEnd) //nolint:errcheck
	}

	_, _ = io.WriteString(e.pw, text)
}

// SendText sends arbitrary text to the terminal.
func (e *Emulator) SendText(text string) {
	_, _ = io.WriteString(e.pw, text)
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
	if e.fgColor == nil {
		return e.defaultFg
	}
	return e.fgColor
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
	if c == nil {
		c = color.White
	}
	e.defaultFg = c
}

// BackgroundColor returns the terminal's background color. This returns nil if
// the background color is not set which means the outer terminal color is
// used.
func (e *Emulator) BackgroundColor() color.Color {
	if e.bgColor == nil {
		return e.defaultBg
	}
	return e.bgColor
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
	if c == nil {
		c = color.Black
	}
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
