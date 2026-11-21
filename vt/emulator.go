package vt

import (
	"encoding/binary"
	"image/color"
	"io"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/parser"
)

// Logger represents a logger interface.
type Logger interface {
	// Printf logs a message formatted as fmt.Printf does.
	Printf(format string, v ...any)
}

// Emulator represents a virtual terminal emulator.
type Emulator struct {
	handlers

	colors         [256]color.Color
	themePal       [16]color.Color
	paletteClaimed bool

	scrs     [2]screen
	scr      *screen
	altSized bool

	cursorStyle  CursorStyle
	cursorSteady bool

	charsets   [4]CharSet
	charsetIDs [4]byte

	logger Logger

	defaultFg, defaultBg, defaultCur color.Color
	fgColor, bgColor, curColor       color.Color
	reportFg, reportBg               color.Color
	guestFg, guestBg                 bool
	reportPal                        [16]color.Color

	modes   ansi.Modes
	modesMu sync.RWMutex

	cachedHasMouse        atomic.Bool
	cachedAllMotion       atomic.Bool
	cachedPixelMode       atomic.Bool
	cachedSyncOutput      atomic.Bool
	cachedAutoWrap        atomic.Bool
	cachedInsertMode      atomic.Bool
	cachedLineFeedNewLine atomic.Bool
	cachedOrigin          atomic.Bool
	syncSetAtNanos        atomic.Int64
	syncOpens             atomic.Uint64
	cachedKittyFlags      atomic.Int32
	kkpUnavailable        atomic.Bool

	lastCluster                 string
	lastClusterWidth            int
	grapheme                    []byte
	rgbCache                    *[256]rgbSlot
	rgbSlab                     []color.RGBA
	clusters                    *[clusterTableLen]string
	lastCellX, lastCellY        int
	lastCellLeft, lastCellRight int
	parkedX, parkedY            int
	openGrapheme                openGrapheme

	parser    *seqParser
	lastState parser.State

	cb Callbacks

	iconName, title string
	explicitIcon    string
	titleStack      []savedTitle
	cwd             string

	tabstops *uv.TabStops

	pipe *bufPipe

	gl, gr  int
	gsingle int

	closed atomic.Bool

	atPhantom bool

	cellWidth  int
	cellHeight int

	kittyMain *KittyState
	kittyAlt  *KittyState

	kittyPassthroughFunc   func(cmd *KittyCommand, rawData []byte)
	kittyHeaderOnly        bool
	kittyImages            KittyGuestImages
	kittyImageIDTranslator KittyImageIDTranslator
	kittyPlaceholderMode   KittyPlaceholderMode
	kittyPlaceholderMemo   kittyPlaceholderMemo

	sixelPassthroughFunc SixelPassthroughFunc
	sixelAdvertised      func() bool

	textSizingFunc func(rawOSC []byte, cursorX, cursorY, scale, textLen int)

	kittyKbd *kittyKeyboardState

	modifyOtherKeys atomic.Int32

	semanticMarkers *SemanticMarkerList

	onReflow func(remap func(absLine int) int)

	pending []byte

	guestCur bool
}

const maxSequenceData = 4 << 20

// NewEmulator creates a new virtual terminal emulator.
func NewEmulator(w, h int) *Emulator {
	t := new(Emulator)
	t.scrs[0] = *newScreen(w, h)
	t.scrs[1] = *newAltScreen()
	t.scr = &t.scrs[0]
	t.scrs[0].cb = &t.cb
	t.scrs[1].cb = &t.cb
	t.parser = newSeqParser(maxSequenceData)
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
	t.parkedX = -1
	t.resetModes()
	t.charsetIDs = defaultCharsetIDs
	t.tabstops = uv.DefaultTabStops(w)
	t.cursorStyle, t.cursorSteady = defaultCursorStyle, defaultCursorSteady

	t.dcsHandlers = make(map[int][]DcsHandler)
	t.csiHandlers = make(map[int][]CsiHandler)
	t.oscHandlers = make(map[int][]OscHandler)
	t.escHandler = make(map[int][]EscHandler)

	t.registerDefaultHandlers()

	t.defaultFg = color.White
	t.defaultBg = color.Black
	t.defaultCur = color.White

	t.kittyMain = NewKittyState()
	t.kittyAlt = NewKittyState()
	t.registerKittyGraphicsHandler()

	t.registerSixelGraphicsHandler()

	t.kittyKbd = newKittyKeyboardState()
	t.registerKittyKeyboardHandlers()
	t.registerModifyOtherKeysHandlers()

	t.semanticMarkers = NewSemanticMarkerList(10000)

	if sb := t.scrs[0].Scrollback(); sb != nil {
		sb.SetOnTrim(func(n int) {
			t.semanticMarkers.AdjustForScrollbackTrim(n)
		})
	}

	return t
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

// SetLogger sets the terminal's logger.
func (e *Emulator) SetLogger(l Logger) {
	e.logger = l
}

// String returns a string representation of the underlying screen buffer.
func (e *Emulator) String() string {
	s := e.scr.buf.String()
	return uv.TrimSpace(StripSixelMarkers(s))
}

const clusterBreak = "\b\x1b[C"

func clustersJoin(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if len(a) == 1 && a[0] < 0x80 && len(b) == 1 && b[0] < 0x80 {
		return false
	}
	last, _ := utf8.DecodeLastRuneInString(a)
	first, _ := utf8.DecodeRuneInString(b)
	if neverJoins(last) && neverJoins(first) {
		return false
	}
	cl, _ := ansi.FirstGraphemeCluster(a+b, ansi.GraphemeWidth)
	return len(cl) > len(a)
}

func neverJoins(r rune) bool {
	return r >= 0x20 && r <= 0x2FF ||
		r >= 0x2100 && r <= 0x2BFF ||
		r >= 0xE000 && r <= 0xF8FF
}

// Render renders a snapshot of the terminal screen as a string with styles and
// links encoded as ANSI escape codes.
//
// The frame must redraw the screen when replayed, and concatenation alone
// cannot: a lone regional indicator in one cell and another in the next are
// two characters on the grid but one flag to any parser reading them back. A
// cluster break separates such neighbours.
func (e *Emulator) Render() string {
	return e.scr.buf.Render()
}

func renderRowBreakingClusters(b *strings.Builder, line uv.Line) {
	var pen uv.Style
	var link uv.Link
	blanks := 0
	prev := ""
	for x := range line {
		c := &line[x]
		if c.IsZero() {
			continue
		}
		if isBlankCell(c) {
			if !pen.IsZero() {
				b.WriteString(ansi.ResetStyle)
				pen = uv.Style{}
			}
			if link.URL != "" {
				b.WriteString(ansi.ResetHyperlink())
				link = uv.Link{}
			}
			blanks++
			prev = " "
			continue
		}
		for ; blanks > 0; blanks-- {
			b.WriteByte(' ')
		}
		if clustersJoin(prev, c.Content) {
			b.WriteString(clusterBreak)
		}
		if c.Style.IsZero() && !pen.IsZero() {
			b.WriteString(ansi.ResetStyle)
			pen = uv.Style{}
		}
		if !penStyleEqual(&c.Style, &pen) {
			b.WriteString(penDiff(&pen, &c.Style))
			pen = c.Style
		}
		if c.Link != link && link.URL != "" {
			b.WriteString(ansi.ResetHyperlink())
			link = uv.Link{}
		}
		if c.Link != link {
			b.WriteString(ansi.SetHyperlink(c.Link.URL, c.Link.Params))
			link = c.Link
		}
		b.WriteString(c.String())
		prev = c.Content
	}
	for ; blanks > 0; blanks-- {
		b.WriteByte(' ')
	}
	if link.URL != "" {
		b.WriteString(ansi.ResetHyperlink())
	}
	if !pen.IsZero() {
		b.WriteString(ansi.ResetStyle)
	}
}

func penDiff(from, to *uv.Style) string {
	if from.Attrs == to.Attrs && from.Underline == to.Underline &&
		penColorEqual(from.UnderlineColor, to.UnderlineColor) {
		fgSame, bgSame := penColorEqual(from.Fg, to.Fg), penColorEqual(from.Bg, to.Bg)
		switch {
		case fgSame && bgSame:
			return ""
		case bgSame:
			if s, ok := paletteSGR(to.Fg, fgSGR); ok {
				return s
			}
		case fgSame:
			if s, ok := paletteSGR(to.Bg, bgSGR); ok {
				return s
			}
		}
	}
	return to.Diff(from)
}

var (
	fgSGR = paletteSGRTable(ansi.Style.ForegroundColor)
	bgSGR = paletteSGRTable(ansi.Style.BackgroundColor)
)

func paletteSGRTable(set func(ansi.Style, ansi.Color) ansi.Style) *[16 + 256]string {
	var t [16 + 256]string
	for i := range 16 {
		t[i] = set(ansi.Style{}, ansi.BasicColor(i)).String()
	}
	for i := range 256 {
		t[16+i] = set(ansi.Style{}, ansi.IndexedColor(i)).String()
	}
	return &t
}

func paletteSGR(c color.Color, table *[16 + 256]string) (string, bool) {
	switch v := c.(type) {
	case ansi.BasicColor:
		if v < 16 {
			return table[v], true
		}
	case ansi.IndexedColor:
		return table[16+int(v)], true
	}
	return "", false
}

func penStyleEqual(a, b *uv.Style) bool {
	return a.Attrs == b.Attrs &&
		a.Underline == b.Underline &&
		penColorEqual(a.Fg, b.Fg) &&
		penColorEqual(a.Bg, b.Bg) &&
		penColorEqual(a.UnderlineColor, b.UnderlineColor)
}

func penColorEqual(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	switch av := a.(type) {
	case ansi.BasicColor:
		if bv, ok := b.(ansi.BasicColor); ok && av == bv {
			return true
		}
	case ansi.IndexedColor:
		if bv, ok := b.(ansi.IndexedColor); ok && av == bv {
			return true
		}
	case ansi.RGBColor:
		if bv, ok := b.(ansi.RGBColor); ok && av == bv {
			return true
		}
	case color.RGBA:
		if bv, ok := b.(color.RGBA); ok && av == bv {
			return true
		}
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

var _ uv.Screen = (*Emulator)(nil)

// Bounds returns the bounds of the terminal.
func (e *Emulator) Bounds() uv.Rectangle {
	return e.scr.Bounds()
}

// AtGround reports whether the parser is between sequences: no escape
// sequence, string or character is half read.
func (e *Emulator) AtGround() bool {
	return e.parser.state == parser.GroundState
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

// PushScrollbackLine appends a line to the main screen's scrollback. It exists
// for snapshot restore, where history arrives as decoded lines rather than as
// a byte stream.
func (e *Emulator) PushScrollbackLine(line uv.Line) {
	e.scrs[0].Scrollback().PushLine(line)
}

// DiscardScrollback makes the main screen drop the lines that scroll off its
// top instead of keeping them. See Scrollback.Discard.
func (e *Emulator) DiscardScrollback() {
	if sb := e.scrs[0].Scrollback(); sb != nil {
		sb.Discard()
	}
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

const (
	maxCommandLines = 64
	maxCommandBytes = 16 << 10
)

func captureCommandText(list *SemanticMarkerList, r markerGridReader, cLine int) string {
	b, prevC := list.commandStartForCapture()
	if prevC != nil {
		return prevC.CapturedText
	}
	if b == nil {
		return ""
	}
	return extractCommandTextFrom(r, b.AbsLine, b.Col, cLine)
}

type markerGridReader interface {
	Width() int
	Height() int
	ScrollbackLen() int
	ScrollbackLine(index int) uv.Line
	CellAt(x, y int) *uv.Cell
	lineSoftWrapped(absLine int) bool
}

func (e *Emulator) lineSoftWrapped(absLine int) bool {
	if sbLen := e.ScrollbackLen(); absLine < sbLen {
		wrapped, _ := e.ScrollbackSoftWrapped(absLine)
		return wrapped
	} else {
		wrapped, _ := e.RowSoftWrapped(absLine - sbLen)
		return wrapped
	}
}

func extractCommandTextFrom(e markerGridReader, bLine, bCol, cLine int) string {
	if cLine-bLine > maxCommandLines || cLine < bLine {
		return ""
	}
	sbLen := e.ScrollbackLen()
	width := e.Width()
	height := e.Height()

	readLine := func(absLine int) string {
		var cells []string
		if absLine < sbLen {
			for _, cell := range e.ScrollbackLine(absLine) {
				cells = append(cells, cell.Content)
			}
		} else {
			screenY := absLine - sbLen
			if screenY < 0 || screenY >= height {
				return ""
			}
			for x := range width {
				content := ""
				if cell := e.CellAt(x, screenY); cell != nil {
					content = cell.Content
				}
				cells = append(cells, content)
			}
		}
		wrapped := e.lineSoftWrapped(absLine)
		var sb strings.Builder
		end := len(cells)
		for end > 0 && (cells[end-1] == "" || (!wrapped && cells[end-1] == " ")) {
			end--
		}
		for _, c := range cells[:end] {
			if c == "" {
				sb.WriteByte(' ')
			} else {
				sb.WriteString(c)
			}
		}
		return sb.String()
	}

	if bLine == cLine || cLine == bLine+1 {
		full := readLine(bLine)
		runes := []rune(full)
		if bCol >= len(runes) {
			return ""
		}
		return capBytes(strings.TrimSpace(string(runes[bCol:])))
	}

	var out, cur strings.Builder
	parts := 0
	if runes := []rune(readLine(bLine)); bCol < len(runes) {
		cur.WriteString(strings.TrimLeft(string(runes[bCol:]), " "))
	}
	endPart := func() {
		if parts > 0 {
			out.WriteByte('\n')
		}
		parts++
		out.WriteString(strings.TrimRight(cur.String(), " "))
		cur.Reset()
	}
	for line := bLine + 1; line < cLine && out.Len()+cur.Len() <= maxCommandBytes; line++ {
		if !e.lineSoftWrapped(line - 1) {
			endPart()
		}
		cur.WriteString(readLine(line))
	}
	endPart()
	return capBytes(out.String())
}

func capBytes(s string) string {
	if len(s) <= maxCommandBytes {
		return s
	}
	n := maxCommandBytes
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return strings.Clone(s[:n])
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
//
// It is always grapheme width, because that is what handleGrapheme measures
// every printed cluster with, whatever DEC mode 2027 says. This is not a free
// choice: ultraviolet asks a uv.Screen which method it uses before building a
// cell to write into it, so answering wcwidth here while placing by grapheme
// width had ultraviolet build a cell one column narrower than the emulator
// would have written for the same text. That happens for exactly two classes,
// a base with an emoji presentation selector and a regional indicator pair,
// and one column is the whole bug: everything after the cluster shifts along
// the row, and in a multiplexer it shifts into the pane next door.
//
// Reporting the method actually in use is the fix. Honouring mode 2027 would
// mean changing placement to match, which is a different and much larger
// change than making the answer true. For the same reason 2027 is permanently
// set: DECRQM answers 3, and a guest that resets it is ignored, so the answer
// stays true after the guest asks to change it. The ghostty backend does honour
// a reset of 2027, and its DECRQM answer says so.
func (e *Emulator) WidthMethod() uv.WidthMethod {
	return ansi.GraphemeWidth
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
//
// A guest with in-band resize reports on (mode 2048) is sent a new report
// when the size in pixels changes, as it is on a resize: it scales SGR-pixel
// mouse reports by the size it was last told.
func (e *Emulator) SetCellSize(width, height int) {
	if width <= 0 || height <= 0 {
		return
	}
	oldW, oldH := e.CellSize()
	e.cellWidth = width
	e.cellHeight = height
	if newW, newH := e.CellSize(); (newW != oldW || newH != oldH) && e.isModeSet(ansi.ModeInBandResize) {
		e.sendInBandResize()
	}
}

// CellSize returns the pixel dimensions of a single character cell, or the
// fallback cell when none was set. It is never zero.
func (e *Emulator) CellSize() (width, height int) {
	return cellSizeOr(e.cellWidth, e.cellHeight)
}

func (e *Emulator) sendInBandResize() {
	cw, ch := e.CellSize()
	h, w := e.Height(), e.Width()
	_, _ = io.WriteString(e.pipe, ansi.InBandResize(h, w, h*ch, w*cw))
}

// CursorPosition returns the terminal's cursor position.
func (e *Emulator) CursorPosition() uv.Position {
	x, y := e.scr.CursorPosition()
	return uv.Pos(x, y)
}

const maxReserveScroll = DefaultScrollbackSize

// ReserveImageSpace reserves space for an image by moving cursor and outputting placeholders.
// This ensures subsequent output appears below the image rather than on top of it.
func (e *Emulator) ReserveImageSpace(rows, cols int) {
	if rows <= 0 {
		return
	}
	_, startY := e.scr.CursorPosition()
	height := e.scr.Height()

	endY := startY + rows
	scrollCount := 0
	if endY > height {
		scrollCount = min(endY-height, maxReserveScroll)
		e.scr.withBlankPen(func() {
			for range scrollCount {
				e.scr.ScrollUp(1)
			}
		})
	}

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
// This is important for mouse event forwarding: mouse events should only be forwarded
// to applications when they are in alternate screen mode.
func (e *Emulator) IsAltScreen() bool {
	return e.isModeSet(ansi.ModeAltScreen) || e.isModeSet(ansi.ModeAltScreenSaveCursor) ||
		e.isModeSet(modeAltScreenLegacy)
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

func (e *Emulator) altScreen() *screen {
	if !e.altSized {
		e.scrs[1].Resize(e.scrs[0].buf.Width(), e.scrs[0].buf.Height())
		e.altSized = true
	}
	return &e.scrs[1]
}

// ReleaseAltScreen drops the alternate screen's cells while the main screen
// is shown. A pane that has run one full-screen program keeps that grid
// otherwise, 1.7 MB at 171x83, and nothing reads it: every switch onto the
// alternate screen clears it first. The screen keeps its size, and a row
// nothing has written reads as blank, so the next switch finds what the clear
// would leave. Dropping the cells at the switch back to the main screen
// instead would make a pager launched over and over allocate its grid each
// time.
func (e *Emulator) ReleaseAltScreen() {
	if e.scr == &e.scrs[1] {
		return
	}
	g := e.scrs[1].buf
	clear(g.rows)
	clear(g.ext)
	clear(g.wrap)
	g.tail = nil
}

// ReleaseScrollbackCache drops the decoded history lines the main screen's
// scrollback keeps for its readers. Scrolling a pane back, hovering a link or
// browsing the history decodes each line it shows and keeps it, up to about
// 7 MB, and after the pane is scrolled down again nothing reads them for the
// rest of the pane's life. The next read decodes the line again, so the
// history reads the same. A line already handed out stays valid, because the
// release drops the cache's map and not the lines in it.
func (e *Emulator) ReleaseScrollbackCache() {
	sb := e.scrs[0].Scrollback()
	if sb == nil {
		return
	}
	sb.cacheMu.Lock()
	sb.cache = nil
	sb.cacheCells = 0
	sb.cacheMu.Unlock()
}

// RestoreAltScreenMode restores the alternate screen mode state.
// This is used when reconnecting to a daemon session to restore the emulator state
// without re-sending the escape sequences that would trigger the mode change.
// This method ONLY switches the screen buffer pointer. It does NOT modify the
// modes map to avoid concurrent map access issues.
func (e *Emulator) RestoreAltScreenMode(enabled bool) {
	if enabled {
		if e.scr != &e.scrs[1] {
			e.scr = e.altScreen()
		}
	} else {
		if e.scr != &e.scrs[0] {
			e.scr = &e.scrs[0]
		}
	}
	if e.kittyKbd != nil {
		e.kittyKbd.SelectScreen(enabled)
		e.updateKittyKeyboardCache()
	}
}

// RestoreCursorPosition puts the cursor back where a restored snapshot had it.
// It is the counterpart of CursorPosition and, like RestoreAltScreenMode, it
// exists so reconnecting does not have to re-send escape sequences whose side
// effects would undo the restore.
func (e *Emulator) RestoreCursorPosition(x, y int) {
	e.setCursor(x, y)
}

// CursorPendingWrap reports whether a wrap is pending at the cursor.
func (e *Emulator) CursorPendingWrap() bool {
	return e.atPhantom
}

// RestoreCursorPendingWrap arms or clears the pending wrap a restored snapshot
// had at the cursor. Call it after RestoreCursorPosition, which clears it.
//
// The cursor is also recorded as standing on the cell it was printed into, as
// the print that armed the wrap left it, so a combining mark that arrives next
// joins that cell rather than the one after it.
func (e *Emulator) RestoreCursorPendingWrap(pending bool) {
	e.atPhantom = pending
	if !pending {
		return
	}
	pos := e.scr.Cursor().Position
	x := pos.X
	if c := e.scr.CellAt(x, pos.Y); x > 0 && c != nil && c.Width == 0 {
		x--
	}
	e.parkedX, e.parkedY = x, pos.Y
}

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
			e.charsets[i] = charsetUK
		case '0':
			e.charsets[i] = charsetSpecialDrawing
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

// CursorStyle returns the shape DECSCUSR last asked for, and whether it is
// steady (not blinking). It reads the terminal-level copy rather than the
// active screen's, because DECSCUSR is a property of the terminal: a guest that
// asks for a bar and then enters the alternate screen still wants a bar. The
// per-screen Cursor.Style is left alone so DECSC/DECRC keep behaving as they
// did.
func (e *Emulator) CursorStyle() (CursorStyle, bool) {
	return e.cursorStyle, e.cursorSteady
}

// RestoreCursorStyle puts back the shape a snapshot was taken under. A pane the
// client is rebuilding from the daemon has emitted its DECSCUSR long ago, so
// without this the pane comes back as a block whatever the guest asked for.
func (e *Emulator) RestoreCursorStyle(style CursorStyle, steady bool) {
	e.cursorStyle, e.cursorSteady = style, steady
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
			continue
		}
		if dec == ansi.ModeSynchronizedOutput {
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

	e.modesMu.Lock()
	for modeNum, enabled := range modes {
		mode := ansi.DECMode(modeNum)
		if !modeRecognised(mode) || modePermanent(mode) {
			continue
		}

		if enabled {
			e.modes[mode] = ansi.ModeSet
		} else {
			e.modes[mode] = ansi.ModeReset
		}
		switch mode {
		case ansi.ModeAutoWrap:
			e.cachedAutoWrap.Store(enabled)
		case ansi.ModeOrigin:
			e.cachedOrigin.Store(enabled)
		}
	}
	e.modesMu.Unlock()

	e.updateMouseModeCache()

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

const syncMaxHold = time.Second

// SyncMaxHold is how long a synchronized update is honored. It is for a host
// that holds a program's output during an update, as the tuios kitty
// passthrough holds a guest's graphics, so it waits no longer than this.
const SyncMaxHold = syncMaxHold

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

// SyncUpdate reports whether the guest has an open synchronized update, as
// IsSyncActive does, and a serial that names that update. Two calls that both
// report open with the same serial saw the same update; a different serial
// means the one seen before has been closed and a new one opened. Thread-safe.
func (e *Emulator) SyncUpdate() (open bool, serial uint64) {
	return e.IsSyncActive(), e.syncOpens.Load()
}

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
	e.cachedPixelMode.Store(hasMouse && e.isModeSet(ansi.ModeMouseExtSgrPixel))
}

// HasPixelMouseMode reports whether the guest tracks the mouse and asked for
// SGR-pixel reports (DEC 1016). Thread-safe: reads the atomic cache.
func (e *Emulator) HasPixelMouseMode() bool {
	return e.cachedPixelMode.Load()
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
	return e.EncodeMouseEventAt(m, MousePixel{})
}

// EncodeMouseEventAt is EncodeMouseEvent with the pointer's pixel position
// inside the pane, which a guest in SGR-pixel mode is told instead of the cell
// centre.
func (e *Emulator) EncodeMouseEventAt(m Mouse, at MousePixel) string {
	r, _, ok := e.mouseReportFor(m, at)
	if !ok {
		return ""
	}
	return r.encode()
}

// Resize resizes the terminal.
func (e *Emulator) Resize(width int, height int) {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}

	if width == e.Width() && height == e.Height() {
		return
	}

	e.openGrapheme = openGrapheme{}
	e.parkedX = -1

	main := &e.scrs[0]
	reflowMain := main.scrollback != nil && main.buf.Width() > 0 &&
		(width != main.buf.Width() ||
			height > main.buf.Height() && main.cur.Y == main.buf.Height()-1 && main.scrollback.Len() > 0) &&
		!main.fitsWithoutReflow(width, height)

	x := e.scr.cur.X
	if e.atPhantom && !(reflowMain && e.scr == main) {
		if x < width-1 {
			e.atPhantom = false
			x++
		}
	}
	x = clamp(x, 0, width-1)

	if reflowMain {
		e.reflowMain(width, height)
		if e.scr == main {
			x = main.cur.X
		}
	} else {
		main.Resize(width, height)
	}
	if e.altSized {
		e.scrs[1].Resize(width, height)
	}
	y := e.scr.cur.Y
	e.tabstops = uv.DefaultTabStops(width)

	phantom := e.atPhantom && (reflowMain && e.scr == main || x == width-1)
	e.setCursor(x, y)
	e.atPhantom = phantom

	if e.isModeSet(ansi.ModeInBandResize) {
		e.sendInBandResize()
	}
}

func (e *Emulator) reflowMain(width, height int) {
	main := &e.scrs[0]
	sb := main.scrollback
	var phantom *bool
	if e.scr == main {
		phantom = &e.atPhantom
	}
	bottom := main.cur.Y == main.buf.Height()-1

	var marks []SemanticMarker
	var points []*reflowPoint
	prompt := -1
	if e.semanticMarkers != nil {
		marks = e.semanticMarkers.Markers()
		points = make([]*reflowPoint, len(marks))
		backing := make([]reflowPoint, len(marks))
		start, typing := -1, false
		for i, m := range marks {
			backing[i] = reflowPoint{abs: m.AbsLine, col: m.Col}
			points[i] = &backing[i]
			switch m.Type {
			case MarkerPromptStart:
				start, typing = m.AbsLine, false
			case MarkerCommandStart:
				typing = start >= 0
			case MarkerCommandExecuted, MarkerCommandFinished:
				start, typing = -1, false
			}
		}
		if typing && start <= sb.Len()+main.cur.Y {
			prompt = start
		}
	}

	sb.holdTrims, sb.heldTrims = true, 0
	remap := main.reflow(width, height, bottom, phantom, prompt, points, e.onReflow != nil)
	trimmed := sb.heldTrims
	sb.holdTrims, sb.heldTrims = false, 0
	onTrim := sb.onTrim

	if e.semanticMarkers != nil {
		for i := range marks {
			marks[i].AbsLine, marks[i].Col = points[i].abs, points[i].col
		}
		e.semanticMarkers.replace(marks)
	}
	if trimmed > 0 && onTrim != nil {
		onTrim(trimmed)
	}
	if e.onReflow != nil {
		e.onReflow(func(abs int) int { return remap(abs) - trimmed })
	}
}

// Read reads data from the terminal input buffer.
func (e *Emulator) Read(p []byte) (n int, err error) {
	if e.closed.Load() {
		return 0, io.EOF
	}

	return e.pipe.Read(p) //nolint:wrapcheck
}

// TakeResponses removes and returns the replies queued for the program, or
// nil when there are none. Unlike Read it never blocks, so a host can drain
// the replies after each Write on the same goroutine. It returns nil after
// Close.
func (e *Emulator) TakeResponses() []byte {
	if e.closed.Load() {
		return nil
	}
	return e.pipe.take()
}

// OnResponse sets a function called each time a reply is queued, on the
// goroutine that queued it. It must not block. nil clears it.
func (e *Emulator) OnResponse(fn func()) { e.pipe.setNotify(fn) }

// Close closes the terminal.
func (e *Emulator) Close() error {
	if e.closed.Load() {
		return nil
	}

	e.closed.Store(true)
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
	from, need := e.parser.State(), 0
	if from == parser.Utf8State {
		need = e.parser.runeNeed()
	}

	for i := 0; i < len(p); i++ {
		if b := p[i]; b >= ansi.SP && b < ansi.DEL && e.parser.State() == parser.GroundState && len(e.grapheme) == 0 {
			j := i + 1
			for j < len(p) && p[j] >= ansi.SP && p[j] < ansi.DEL {
				j++
			}
			e.printASCIIRun(p[i:j])
			e.lastState = parser.GroundState
			i = j - 1
			continue
		}
		if st := e.parser.State(); (st == parser.ApcStringState || st == parser.DcsStringState) &&
			isStringPayload(p[i]) && len(e.grapheme) == 0 {
			j := i + 1 + stringPayloadRun(p[i+1:])
			e.parser.putRun(p[i:j])
			e.lastState = st
			i = j - 1
			continue
		}
		if e.parser.State() == parser.Utf8State && p[i]&0xc0 != 0x80 {
			e.lastState = parser.GroundState
		}
		e.parser.Advance(p[i])
		state := e.parser.State()
		if len(e.grapheme) > 0 {
			if e.lastState == parser.GroundState && state != parser.Utf8State {
				e.flushGraphemeAtWriteEnd()
			} else if i == len(p)-1 {
				e.flushGraphemeAtWriteEnd()
			}
		}
		e.lastState = state
	}
	e.notePending(p, from, need)
	return len(p), nil
}

func isStringPayload(b byte) bool {
	return b >= 0x20
}

func stringPayloadRun(p []byte) int {
	const (
		ones  = 0x0101010101010101
		highs = 0x8080808080808080
	)
	n := 0
	for len(p)-n >= 8 {
		x := binary.LittleEndian.Uint64(p[n:])
		if (x-0x20*ones)&^x&highs != 0 {
			break
		}
		n += 8
	}
	for n < len(p) && isStringPayload(p[n]) {
		n++
	}
	return n
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
	return color.White
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
	return color.Black
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

// SetReportColors sets the colours an OSC 10 and OSC 11 query is answered with
// while the guest has not set its own, and the colours an OSC 4 query for one
// of the sixteen ANSI slots is answered with while neither the guest nor a
// theme has set the slot. A nil colour keeps the default answer.
//
// When any of them changes and the guest turned on mode 2031, the guest is
// sent one colour scheme notice for the whole change. See color_scheme.go.
func (e *Emulator) SetReportColors(fg, bg color.Color, pal [16]color.Color) {
	changed := !sameReport(e.reportFg, e.reportBg, e.reportPal, fg, bg, pal)
	e.reportFg, e.reportBg, e.reportPal = fg, bg, pal
	if changed && e.isModeSet(ansi.ModeLightDark) {
		_, _ = io.WriteString(e.pipe, colorSchemeNotice(e.reportedBackground()))
	}
}

func (e *Emulator) reportedIndexedColor(i int) color.Color {
	if i >= 0 && i < 16 && e.paletteEntry(i) == nil && e.reportPal[i] != nil {
		return e.reportPal[i]
	}
	return e.IndexedColor(i)
}

func (e *Emulator) reportedForeground() color.Color {
	if !e.guestFg && e.reportFg != nil {
		return e.reportFg
	}
	return e.ForegroundColor()
}

func (e *Emulator) reportedBackground() color.Color {
	if !e.guestBg && e.reportBg != nil {
		return e.reportBg
	}
	return e.BackgroundColor()
}

// CursorColor returns the terminal's cursor color. This returns nil if the
// cursor color is not set which means the outer terminal color is used.
func (e *Emulator) CursorColor() color.Color {
	if e.curColor == nil {
		return e.defaultCur
	}
	return e.curColor
}

// GuestCursorColor is the cursor colour the guest set with OSC 12, or nil
// when it set none or reset it with OSC 112.
func (e *Emulator) GuestCursorColor() color.Color {
	if !e.guestCur {
		return nil
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

	c := e.paletteEntry(i)
	if c == nil {
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
	if c := e.paletteEntry(i); c != nil {
		return c
	}
	// #nosec G115 - i is validated to be in [0, 15] above
	return ansi.BasicColor(uint8(i))
}

func (e *Emulator) paletteEntry(i int) color.Color {
	if i < 0 || i > 255 {
		return nil
	}
	if c := e.colors[i]; c != nil {
		return c
	}
	if i < 16 {
		return e.themePal[i]
	}
	return nil
}

// SetIndexedColor sets a terminal's indexed color.
// The index must be between 0 and 255.
func (e *Emulator) SetIndexedColor(i int, c color.Color) {
	if i < 0 || i > 255 {
		return
	}

	e.colors[i] = c
	e.refreshPaletteClaims()
}

func (e *Emulator) refreshPaletteClaims() {
	e.paletteClaimed = false
	for i := range 16 {
		if e.colors[i] != nil || e.themePal[i] != nil {
			e.paletteClaimed = true
			return
		}
	}
}

// SetThemeColors sets the terminal's color palette from a theme.
// This sets the default foreground, background, cursor colors and the
// first 16 ANSI colors (0-15) which are used by terminal applications.
//
// A nil fg and bg mean no theme is active. That has to put the sixteen back the
// way they were, not merely stop writing them: a theme the user has turned off
// that stays in the color table goes on painting every pane in its own red and
// blue, which is the "going back to none messes up the ANSI 16" report.
func (e *Emulator) SetThemeColors(fg, bg, cur color.Color, ansiPalette [16]color.Color) {
	e.SetDefaultForegroundColor(fg)
	e.SetDefaultBackgroundColor(bg)
	e.SetDefaultCursorColor(cur)

	if fg == nil && bg == nil {
		e.themePal = [16]color.Color{}
	} else {
		e.themePal = ansiPalette
	}
	e.refreshPaletteClaims()
}

func (e *Emulator) hasThemeColors() bool {
	return e.paletteClaimed
}

func (e *Emulator) resetTabStops() {
	e.tabstops = uv.DefaultTabStops(e.Width())
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

		cmd, rawData, err := parseKittyAPC(data, e.kittyHeaderOnly)
		if err != nil || cmd == nil {
			return false
		}

		if cmd.PayloadErr != nil {
			if resp := KittyPayloadErrorResponse(cmd); resp != nil {
				_, _ = e.pipe.Write(resp)
			}
			if cmd.Action == KittyActionQuery {
				return true
			}
		}

		if !e.kittyImages.Handle(cmd, func(b []byte) { _, _ = e.pipe.Write(b) }) {
			return true
		}

		if e.kittyPassthroughFunc != nil {
			e.kittyPassthroughFunc(cmd, rawData)
			return true
		}

		if cmd.Action == KittyActionQuery {
			_, _ = e.pipe.Write(BuildKittyResponse(true, cmd.ImageID, ""))
		}
		return true
	})
}

func (e *Emulator) logf(format string, v ...any) {
	if e.logger != nil {
		e.logger.Printf(format, v...)
	}
}

// SetKittyImageIDTranslator installs the guest-to-host image id mapping used
// for kitty Unicode placeholder cells. Nil leaves every cell as the guest
// wrote it.
func (e *Emulator) SetKittyImageIDTranslator(fn KittyImageIDTranslator) {
	e.kittyImageIDTranslator = fn
}

// SetKittyPlaceholderMode says whether placeholder cells are kept or dropped.
func (e *Emulator) SetKittyPlaceholderMode(m KittyPlaceholderMode) {
	e.kittyPlaceholderMode = m
}

// SetKittyPassthroughFunc installs the reader of every graphics command. fn
// must not write to rawData: cmd.RawPayload shares its bytes.
func (e *Emulator) SetKittyPassthroughFunc(fn func(cmd *KittyCommand, rawData []byte)) {
	e.kittyPassthroughFunc = fn
}

// SetKittyUploadAnswers says whether this emulator answers kitty uploads and
// placements. See KittyGuestImages.
func (e *Emulator) SetKittyUploadAnswers(on bool) { e.kittyImages.SetAnswers(on) }

// SetKittyFilesElsewhere says the pane runs on another machine. See
// KittyGuestImages.SetFilesElsewhere.
func (e *Emulator) SetKittyFilesElsewhere(on bool) { e.kittyImages.SetFilesElsewhere(on) }

// KittyGuestImagesState returns the ids given to the guest's image numbers,
// for a client that attaches. Nil when there are none.
func (e *Emulator) KittyGuestImagesState() *KittyGuestImagesState { return e.kittyImages.State() }

// RestoreKittyGuestImages takes the daemon's ids for the guest's image
// numbers. Nil leaves them as they are.
func (e *Emulator) RestoreKittyGuestImages(s *KittyGuestImagesState) { e.kittyImages.Restore(s) }

// SetKittyHeaderOnly says whether the passthrough acts on the control keys
// alone. When it does, commands are parsed with ParseKittyHeader and the
// passthrough gets a nil rawData.
func (e *Emulator) SetKittyHeaderOnly(on bool) {
	e.kittyHeaderOnly = on
}

func kittyRawAPC(data []byte) []byte {
	rawData := make([]byte, len(data)+4)
	rawData[0] = '\x1b'
	rawData[1] = '_'
	copy(rawData[2:], data)
	rawData[len(rawData)-2] = '\x1b'
	rawData[len(rawData)-1] = '\\'
	return rawData
}

// KittyState returns the kitty image state of the active screen.
func (e *Emulator) KittyState() *KittyState {
	if e.IsAltScreen() {
		return e.kittyAlt
	}
	return e.kittyMain
}

// KittyMainState returns the kitty image state of the main screen.
func (e *Emulator) KittyMainState() *KittyState {
	return e.kittyMain
}

// KittyAltState returns the kitty image state of the alternate screen.
func (e *Emulator) KittyAltState() *KittyState {
	return e.kittyAlt
}

func (e *Emulator) registerSixelGraphicsHandler() {
	e.RegisterDcsHandler(int('q'), func(params ansi.Params, data []byte) bool {
		var fullData []byte

		for i, p := range params {
			if i > 0 {
				fullData = append(fullData, ';')
			}
			val := p.Param(0)
			if val == 0 {
				fullData = append(fullData, '0')
			} else {
				digits := make([]byte, 0, 10)
				for val > 0 {
					digits = append(digits, byte('0'+val%10))
					val /= 10
				}
				for _, digit := range slices.Backward(digits) {
					fullData = append(fullData, digit)
				}
			}
		}

		fullData = append(fullData, 'q')
		fullData = append(fullData, data...)

		cmd := ParseSixelCommand(fullData)
		if cmd == nil {
			return false
		}

		cellWidth, cellHeight := e.CellSize()
		cmd.CellWidth, cmd.CellHeight = cellWidth, cellHeight
		rows, cols := SixelCells(cmd, cellWidth, cellHeight)
		cursorX, cursorY := e.scr.CursorPosition()
		var id uint32
		if e.sixelPassthroughFunc != nil {
			id = e.sixelPassthroughFunc(cmd, cursorX, cursorY)
		}
		e.placeSixel(rows, cols, id)
		return true
	})
}

func (e *Emulator) placeSixel(rows, cols int, id uint32) {
	e.placeImage(rows, cols, id, true)
}

// PlaceImageMarkers writes the marker cells of image id, rows by cols, at the
// cursor, as a sixel's are written: a kitty image drawn as glyphs on a host
// without graphics (internal/app). With move the cursor ends at column 0 of
// the row under the image, scrolling first so that row exists, as
// ReserveImageSpace leaves it for a kitty image a host draws. Without (C=1)
// nothing scrolls and the cursor stays, and rows past the screen are not
// written.
func (e *Emulator) PlaceImageMarkers(rows, cols int, id uint32, move bool) {
	e.placeImage(rows, cols, id, move)
}

// EraseImageMarkers blanks every marker cell on the screen whose image id
// match accepts, keeping its background: a kitty delete (a=d) of a picture
// drawn as glyphs.
func (e *Emulator) EraseImageMarkers(match func(id uint32) bool) {
	height, width := e.scr.Height(), e.scr.Width()
	for y := range height {
		for x := range width {
			old := e.scr.CellAt(x, y)
			if old == nil {
				continue
			}
			id, _, _, ok := ParseSixelMarker(old.Content)
			if !ok || !match(id) {
				continue
			}
			cell := uv.Cell{Content: " ", Width: 1}
			cell.Style.Bg = old.Style.Bg
			e.scr.SetCell(x, y, &cell)
		}
	}
}

func (e *Emulator) placeImage(rows, cols int, id uint32, move bool) {
	if rows <= 0 || cols <= 0 {
		return
	}
	startX, startY := e.scr.CursorPosition()
	height, width := e.scr.Height(), e.scr.Width()
	scroll := 0
	if move && startY+rows+1 > height {
		scroll = min(startY+rows+1-height, height)
		e.scr.withBlankPen(func() {
			for range scroll {
				e.scr.ScrollUp(1)
			}
		})
	}
	top := startY - scroll
	for r := range rows {
		y := top + r
		if y < 0 || y >= height {
			continue
		}
		for c := range cols {
			x := startX + c
			if x >= width {
				break
			}
			cell := uv.Cell{Content: SixelMarker(id, r, c), Width: 1}
			if old := e.scr.CellAt(x, y); old != nil {
				cell.Style.Bg = old.Style.Bg
			}
			e.scr.SetCell(x, y, &cell)
		}
	}
	if !move {
		e.scr.setCursor(startX, startY, false)
		return
	}
	e.scr.setCursor(0, min(startY+rows-scroll, height-1), false)
}

// SetSixelPassthroughFunc installs the function that decodes a guest's sixel
// image and returns the id its marker cells carry. See sixel_marker.go.
func (e *Emulator) SetSixelPassthroughFunc(fn SixelPassthroughFunc) {
	e.sixelPassthroughFunc = fn
}

// SetSixelAdvertised installs the function that decides whether DA1 lists
// sixel and XTSMGRAPHICS answers. Nil advertises nothing.
func (e *Emulator) SetSixelAdvertised(fn func() bool) {
	e.sixelAdvertised = fn
}

func (e *Emulator) sixelOn() bool {
	return e.sixelAdvertised != nil && e.sixelAdvertised()
}

// SetReflowFunc sets the function a reflow calls with where rows went. See
// Terminal.SetReflowFunc.
func (e *Emulator) SetReflowFunc(fn func(remap func(absLine int) int)) {
	e.onReflow = fn
}

// SetTextSizingFunc installs the function that takes each OSC 66 text
// sizing sequence, rebuilt for the host terminal, with the cursor position,
// the scale and the length of the text in characters.
func (e *Emulator) SetTextSizingFunc(fn func(rawOSC []byte, cursorX, cursorY, scale, textLen int)) {
	e.textSizingFunc = fn
}
