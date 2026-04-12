// Package ghostty wraps github.com/mitchellh/go-libghostty for use as
// the daemon-side VT emulator in tuios. It provides a thread-safe
// DaemonTerminal that processes PTY output through libghostty's VT parser
// and produces screen diffs via the render state API with dirty tracking.
package ghostty

import (
	"sync"
	"time"

	libghostty "github.com/mitchellh/go-libghostty"
)

// ScreenDiff represents a set of changed cells on the terminal screen.
type ScreenDiff struct {
	Cells        []DiffCell
	CursorX      int
	CursorY      int
	CursorHidden bool
	IsAltScreen  bool
	Width        int
	Height       int
}

// DiffCell is a single changed cell with position and style.
type DiffCell struct {
	Row     int
	Col     int
	Content string
	Width   int
	Fg      uint32 // RGBA packed (0 = default)
	Bg      uint32 // RGBA packed (0 = default)
	Attrs   uint16 // bitmask: bold|faint|italic|reverse|blink|rapid_blink|conceal|strikethrough
	UlColor uint32 // underline color RGBA
	UlStyle uint8  // underline style
}

// Attribute bitmask constants (matching session.DiffAttr* and ultraviolet).
const (
	AttrBold          uint16 = 1 << 0
	AttrFaint         uint16 = 1 << 1
	AttrItalic        uint16 = 1 << 2
	AttrReverse       uint16 = 1 << 3
	AttrBlink         uint16 = 1 << 4
	AttrRapidBlink    uint16 = 1 << 5
	AttrConceal       uint16 = 1 << 6
	AttrStrikethrough uint16 = 1 << 7
)

// cellState is a compact cell representation for snapshot comparison.
// Stored in flat arrays for cache-friendly diffing.
type cellState struct {
	content string
	fg      uint32
	bg      uint32
	attrs   uint16
	ulColor uint32
	ulStyle uint8
	width   int8
}

// DaemonTerminal wraps a libghostty Terminal for daemon-side VT processing.
// It is thread-safe for Write/Resize from any goroutine.
type DaemonTerminal struct {
	mu    sync.Mutex
	term  *libghostty.Terminal
	rs    *libghostty.RenderState
	ri    *libghostty.RenderStateRowIterator
	rc    *libghostty.RenderStateRowCells
	dirty chan struct{} // signal channel (cap 1) for event-driven diffs

	// Async write channel for non-blocking writes from PTY reader.
	writeCh chan []byte

	// Double-buffered screen state — no allocations after first frame.
	cells        []cellState // current screen (reused)
	width        int
	height       int
	prevCursorX  int
	prevCursorY  int
	prevCursorHidden bool

	// Pre-allocated diff output buffer (reused, grown as needed).
	diffBuf []DiffCell

	// Coalesce timer for batching rapid updates.
	coalesceDur time.Duration
}

// NewDaemonTerminal creates a ghostty-backed terminal for a daemon PTY.
func NewDaemonTerminal(cols, rows int) (*DaemonTerminal, error) {
	term, err := libghostty.NewTerminal(
		libghostty.WithSize(uint16(cols), uint16(rows)),
		libghostty.WithMaxScrollback(10000),
	)
	if err != nil {
		return nil, err
	}

	rs, err := libghostty.NewRenderState()
	if err != nil {
		term.Close()
		return nil, err
	}

	ri, err := libghostty.NewRenderStateRowIterator()
	if err != nil {
		rs.Close()
		term.Close()
		return nil, err
	}

	rc, err := libghostty.NewRenderStateRowCells()
	if err != nil {
		ri.Close()
		rs.Close()
		term.Close()
		return nil, err
	}

	size := cols * rows
	dt := &DaemonTerminal{
		term:        term,
		rs:          rs,
		ri:          ri,
		rc:          rc,
		dirty:       make(chan struct{}, 1),
		writeCh:     make(chan []byte, 256),
		cells:       make([]cellState, size),
		width:       cols,
		height:      rows,
		diffBuf:     make([]DiffCell, 0, size),
		coalesceDur: 2 * time.Millisecond,
	}
	go dt.writerLoop()
	return dt, nil
}

// Free releases all resources.
func (dt *DaemonTerminal) Free() {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	if dt.rc != nil {
		dt.rc.Close()
		dt.rc = nil
	}
	if dt.ri != nil {
		dt.ri.Close()
		dt.ri = nil
	}
	if dt.rs != nil {
		dt.rs.Close()
		dt.rs = nil
	}
	if dt.term != nil {
		dt.term.Close()
		dt.term = nil
	}
}

// Write processes PTY output through the VT parser and signals dirty.
func (dt *DaemonTerminal) Write(data []byte) {
	dt.mu.Lock()
	if dt.term != nil {
		dt.term.VTWrite(data)
	}
	dt.mu.Unlock()

	select {
	case dt.dirty <- struct{}{}:
	default:
	}
}

// WriteNonBlocking sends data to a channel for async processing.
// Drops if the channel is full.
func (dt *DaemonTerminal) WriteNonBlocking(data []byte) {
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)
	select {
	case dt.writeCh <- dataCopy:
	default:
	}
}

// writerLoop drains the write channel and feeds data to the terminal.
// Batches consecutive writes to reduce lock contention.
func (dt *DaemonTerminal) writerLoop() {
	for data := range dt.writeCh {
		func() {
			defer func() {
				if r := recover(); r != nil {
					// CGo panic — don't crash the daemon.
				}
			}()
			// Batch: drain any additional pending writes while we hold the lock.
			dt.mu.Lock()
			if dt.term != nil {
				dt.term.VTWrite(data)
				for {
					select {
					case more := <-dt.writeCh:
						dt.term.VTWrite(more)
					default:
						goto done
					}
				}
			done:
			}
			dt.mu.Unlock()

			select {
			case dt.dirty <- struct{}{}:
			default:
			}
		}()
	}
}

// Resize changes the terminal dimensions.
func (dt *DaemonTerminal) Resize(cols, rows int) error {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	if dt.term != nil {
		return dt.term.Resize(uint16(cols), uint16(rows), 0, 0)
	}
	return nil
}

// DirtySignal returns the channel signaled when new content is available.
func (dt *DaemonTerminal) DirtySignal() <-chan struct{} {
	return dt.dirty
}

// ReadDiff updates the render state and computes a screen diff containing
// only cells that changed since the last call. Returns nil if nothing changed.
//
// Performance critical path. Key optimizations:
//   - Only iterates dirty rows via libghostty's per-row dirty tracking
//   - Clean rows are skipped entirely (zero CGo calls)
//   - Pre-allocated buffers reused across calls (zero allocations after warmup)
//   - Single pass: read cell + compare + emit diff in one loop
//
// Must be called from a single goroutine (the diff streamer).
func (dt *DaemonTerminal) ReadDiff() *ScreenDiff {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	if dt.term == nil || dt.rs == nil {
		return nil
	}

	// Update render state from terminal (consumes dirty flags).
	if err := dt.rs.Update(dt.term); err != nil {
		return nil
	}

	// Check dirty state.
	dirtyState, err := dt.rs.Dirty()
	if err != nil || dirtyState == libghostty.RenderStateDirtyFalse {
		return nil
	}
	fullRedraw := dirtyState == libghostty.RenderStateDirtyFull

	// Get dimensions.
	cols, _ := dt.rs.Cols()
	rows, _ := dt.rs.Rows()
	w := int(cols)
	h := int(rows)
	size := w * h

	// Get cursor.
	var cursorX, cursorY int
	var cursorHidden bool
	cursorVis, _ := dt.rs.CursorVisible()
	cursorHidden = !cursorVis
	if hasVal, _ := dt.rs.CursorViewportHasValue(); hasVal {
		cx, _ := dt.rs.CursorViewportX()
		cy, _ := dt.rs.CursorViewportY()
		cursorX = int(cx)
		cursorY = int(cy)
	}

	// Get alt screen state (needed for mouse event routing on client).
	var isAltScreen bool
	if activeScreen, err := dt.term.ActiveScreen(); err == nil {
		isAltScreen = activeScreen == libghostty.ScreenAlternate
	}

	// Handle resize: grow buffer, mark full redraw.
	if dt.width != w || dt.height != h || len(dt.cells) != size {
		fullRedraw = true
		if cap(dt.cells) >= size {
			dt.cells = dt.cells[:size]
		} else {
			dt.cells = make([]cellState, size)
		}
		// Zero out new cells.
		clear(dt.cells)
		dt.width = w
		dt.height = h
	}

	// Get colors for palette resolution.
	colors, err := dt.rs.Colors()
	if err != nil {
		return nil
	}

	// Populate row iterator.
	if err := dt.rs.RowIterator(dt.ri); err != nil {
		return nil
	}

	// Reuse diff output buffer.
	diff := dt.diffBuf[:0]

	y := 0
	for dt.ri.Next() {
		if y >= h {
			break
		}

		rowDirty, _ := dt.ri.Dirty()
		if !rowDirty && !fullRedraw {
			// Row is clean — cells haven't changed. Skip entirely.
			y++
			continue
		}

		// Row is dirty — read cells via CGo and diff against stored state.
		if err := dt.ri.Cells(dt.rc); err != nil {
			y++
			continue
		}

		rowOff := y * w
		x := 0
		for dt.rc.Next() {
			if x >= w {
				break
			}

			idx := rowOff + x
			prev := dt.cells[idx]
			var cur cellState

			// Read grapheme content.
			graphemes, _ := dt.rc.Graphemes()
			if len(graphemes) > 0 {
				cur.content = codePointToString(graphemes)
				cur.width = 1
			} else {
				cur.width = 1
			}

			// Read style (one CGo call gives us all attributes).
			if style, err := dt.rc.Style(); err == nil {
				cur.attrs = styleAttrs(style)
				cur.ulStyle = uint8(style.Underline())

				// Resolve fg color: prefer resolved color, fall back to style.
				if fgColor, err := dt.rc.FgColor(); err == nil && fgColor != nil {
					cur.fg = packRGB(fgColor)
				} else {
					cur.fg = resolveStyleColor(style.FgColor(), colors)
				}

				// Resolve bg color: prefer resolved color, fall back to style.
				if bgColor, err := dt.rc.BgColor(); err == nil && bgColor != nil {
					cur.bg = packRGB(bgColor)
				} else {
					cur.bg = resolveStyleColor(style.BgColor(), colors)
				}

				// Resolve underline color.
				cur.ulColor = resolveStyleColor(style.UnderlineColor(), colors)
			}

			// Compare and emit diff only if changed.
			if cur != prev {
				dt.cells[idx] = cur
				diff = append(diff, DiffCell{
					Row: y, Col: x,
					Content: cur.content, Width: int(cur.width),
					Fg: cur.fg, Bg: cur.bg,
					Attrs: cur.attrs, UlColor: cur.ulColor, UlStyle: cur.ulStyle,
				})
			}

			x++
		}

		// Clear row dirty flag.
		_ = dt.ri.SetDirty(false)
		y++
	}

	// Reset global dirty state.
	_ = dt.rs.SetDirty(libghostty.RenderStateDirtyFalse)

	cursorChanged := cursorX != dt.prevCursorX || cursorY != dt.prevCursorY ||
		cursorHidden != dt.prevCursorHidden

	if len(diff) == 0 && !cursorChanged {
		dt.diffBuf = diff // preserve capacity
		return nil
	}

	dt.prevCursorX = cursorX
	dt.prevCursorY = cursorY
	dt.prevCursorHidden = cursorHidden

	// Return a copy of cells (diffBuf is reused next call).
	result := make([]DiffCell, len(diff))
	copy(result, diff)
	dt.diffBuf = diff // preserve capacity for next call

	return &ScreenDiff{
		Cells:        result,
		CursorX:      cursorX,
		CursorY:      cursorY,
		CursorHidden: cursorHidden,
		IsAltScreen:  isAltScreen,
		Width:        w,
		Height:       h,
	}
}

// CoalesceDuration returns the coalesce duration for the diff streamer.
func (dt *DaemonTerminal) CoalesceDuration() time.Duration {
	return dt.coalesceDur
}

// Title returns the terminal title set via OSC 0/2.
func (dt *DaemonTerminal) Title() string {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	if dt.term == nil {
		return ""
	}
	title, _ := dt.term.Title()
	return title
}

// Pwd returns the terminal working directory set via OSC 7.
func (dt *DaemonTerminal) Pwd() string {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	if dt.term == nil {
		return ""
	}
	pwd, _ := dt.term.Pwd()
	return pwd
}

// codePointToString converts unicode codepoints to a Go string.
// Fast path for single ASCII codepoint (the common case).
func codePointToString(codepoints []uint32) string {
	if len(codepoints) == 1 {
		cp := codepoints[0]
		if cp < 128 {
			return string(rune(cp))
		}
		return string(rune(cp))
	}
	runes := make([]rune, len(codepoints))
	for i, cp := range codepoints {
		runes[i] = rune(cp)
	}
	return string(runes)
}

// packRGB packs a ColorRGB into a uint32 RGBA value (alpha=255).
func packRGB(c *libghostty.ColorRGB) uint32 {
	return uint32(c.R)<<24 | uint32(c.G)<<16 | uint32(c.B)<<8 | 255
}

// resolveStyleColor resolves a StyleColor to a packed RGBA uint32 using the palette.
// Returns 0 for unset colors (meaning "default terminal color").
func resolveStyleColor(sc libghostty.StyleColor, colors *libghostty.RenderStateColors) uint32 {
	switch sc.Tag {
	case libghostty.StyleColorRGB:
		return packRGB(&sc.RGB)
	case libghostty.StyleColorPalette:
		c := colors.Palette[sc.Palette]
		return packRGB(&c)
	default:
		return 0
	}
}

// styleAttrs extracts attribute flags from a libghostty Style.
func styleAttrs(s *libghostty.Style) uint16 {
	var attrs uint16
	if s.Bold() {
		attrs |= AttrBold
	}
	if s.Faint() {
		attrs |= AttrFaint
	}
	if s.Italic() {
		attrs |= AttrItalic
	}
	if s.Inverse() {
		attrs |= AttrReverse
	}
	if s.Blink() {
		attrs |= AttrBlink
	}
	if s.Invisible() {
		attrs |= AttrConceal
	}
	if s.Strikethrough() {
		attrs |= AttrStrikethrough
	}
	return attrs
}
