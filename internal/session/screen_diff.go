package session

import (
	"encoding/binary"
	"image/color"
	"sync"
	"sync/atomic"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// ScreenDiff represents a set of changed cells on the terminal screen.
// It is the unit of data sent from daemon to client in the event-based
// screen diff protocol. Each diff is a partial screen update containing
// only cells that changed since the last delivered diff.
type ScreenDiff struct {
	Cells         []DiffCell
	CursorX       int
	CursorY       int
	CursorHidden  bool
	CursorStyle   vt.CursorStyle
	IsAltScreen   bool
	HasMouseMode  bool // True if PTY application has mouse tracking enabled
	Title         string
	Width, Height int
}

// DiffCell is a single changed cell with its position and full style.
type DiffCell struct {
	Row     int
	Col     int
	Content string
	Width   int
	Fg      uint32 // RGBA packed (0 = default)
	Bg      uint32 // RGBA packed (0 = default)
	Attrs   uint16 // bitmask of style attributes
	UlColor uint32 // underline color RGBA
	UlStyle uint8  // underline style
}

// Attribute bitmask constants matching ultraviolet's Attr type.
const (
	DiffAttrBold          uint16 = 1 << 0
	DiffAttrFaint         uint16 = 1 << 1
	DiffAttrItalic        uint16 = 1 << 2
	DiffAttrReverse       uint16 = 1 << 3
	DiffAttrBlink         uint16 = 1 << 4
	DiffAttrRapidBlink    uint16 = 1 << 5
	DiffAttrConceal       uint16 = 1 << 6
	DiffAttrStrikethrough uint16 = 1 << 7
)

// packColor converts a color.Color to a packed RGBA uint32.
// Returns 0 for nil (meaning "default terminal color").
func packColor(c color.Color) uint32 {
	if c == nil {
		return 0
	}
	r, g, b, a := c.RGBA()
	return uint32(r>>8)<<24 | uint32(g>>8)<<16 | uint32(b>>8)<<8 | uint32(a>>8)
}


// ---- Snapshot-based screen differ ----
//
// Instead of relying on ultraviolet's Touched bitmap (which misses scrolls,
// clears, insert/delete line operations), we take a full screen snapshot and
// compare it against the previous one. Only cells that actually changed get
// included in the diff. This is correct for ALL terminal operations.
//
// Performance: comparing 200x50 = 10,000 cells is ~50us on modern hardware
// (cache-friendly sequential memory scan). Well within budget even at 300fps.

// snapshotCell is a compact cell representation for snapshot comparison.
// Stored in flat arrays for cache-friendly comparison.
type snapshotCell struct {
	Content string
	Width   int8
	Fg      uint32
	Bg      uint32
	Attrs   uint8
	UlColor uint32
	UlStyle uint8
}

func cellToSnapshot(cell *uv.Cell) snapshotCell {
	if cell == nil {
		return snapshotCell{}
	}
	return snapshotCell{
		Content: cell.Content,
		Width:   int8(cell.Width),
		Fg:      packColor(cell.Style.Fg),
		Bg:      packColor(cell.Style.Bg),
		Attrs:   cell.Style.Attrs,
		UlColor: packColor(cell.Style.UnderlineColor),
		UlStyle: uint8(cell.Style.Underline),
	}
}

func (a snapshotCell) eq(b snapshotCell) bool {
	return a.Content == b.Content &&
		a.Width == b.Width &&
		a.Fg == b.Fg &&
		a.Bg == b.Bg &&
		a.Attrs == b.Attrs &&
		a.UlColor == b.UlColor &&
		a.UlStyle == b.UlStyle
}

// ScreenDiffer computes diffs between consecutive screen states.
// Owned by one subscriber goroutine; not thread-safe itself.
// Buffers are pre-allocated and reused across calls to avoid GC pressure.
type ScreenDiffer struct {
	prev          []snapshotCell // previous screen state
	current       []snapshotCell // reusable buffer for current snapshot
	diffBuf       []DiffCell     // reusable buffer for diff output
	prevWidth     int
	prevHeight    int
	prevCursorX   int
	prevCursorY   int
	prevCursorHid bool
	prevAltScreen bool
}

// NewScreenDiffer creates a differ with no previous state (first diff
// will be a full screen snapshot).
func NewScreenDiffer() *ScreenDiffer {
	return &ScreenDiffer{}
}

// ComputeDiff reads the current screen from the VT emulator (caller must
// hold the terminalMu read lock) and returns a ScreenDiff containing only
// cells that changed since the last call. Returns nil if nothing changed.
// Pre-allocated buffers are reused across calls to avoid GC pressure.
func (d *ScreenDiffer) ComputeDiff(em *vt.Emulator) *ScreenDiff {
	w := em.Width()
	h := em.Height()
	size := w * h
	pos := em.CursorPosition()
	cursorHidden := em.IsCursorHidden()
	isAlt := em.IsAltScreen()

	// Reuse or grow the current snapshot buffer
	if cap(d.current) < size {
		d.current = make([]snapshotCell, size)
	}
	current := d.current[:size]

	// Read current screen into pre-allocated buffer
	for y := range h {
		off := y * w
		for x := range w {
			cell := em.CellAt(x, y)
			if cell != nil {
				current[off+x] = cellToSnapshot(cell)
			} else {
				current[off+x] = snapshotCell{}
			}
		}
	}

	// Reuse cells slice (reset length, keep capacity)
	cells := d.diffBuf[:0]
	sameSize := d.prevWidth == w && d.prevHeight == h

	if sameSize && len(d.prev) == size {
		for y := range h {
			off := y * w
			for x := range w {
				idx := off + x
				if !current[idx].eq(d.prev[idx]) {
					sc := &current[idx]
					cells = append(cells, DiffCell{
						Row: y, Col: x,
						Content: sc.Content,
						Width:   int(sc.Width),
						Fg:      sc.Fg, Bg: sc.Bg,
						Attrs:   uint16(sc.Attrs),
						UlColor: sc.UlColor, UlStyle: sc.UlStyle,
					})
				}
			}
		}
	} else {
		for y := range h {
			off := y * w
			for x := range w {
				sc := &current[off+x]
				cells = append(cells, DiffCell{
					Row: y, Col: x,
					Content: sc.Content,
					Width:   int(sc.Width),
					Fg:      sc.Fg, Bg: sc.Bg,
					Attrs:   uint16(sc.Attrs),
					UlColor: sc.UlColor, UlStyle: sc.UlStyle,
				})
			}
		}
	}
	d.diffBuf = cells // save grown slice for next reuse

	cursorChanged := pos.X != d.prevCursorX || pos.Y != d.prevCursorY ||
		cursorHidden != d.prevCursorHid || isAlt != d.prevAltScreen

	if len(cells) == 0 && !cursorChanged {
		// Swap buffers for next comparison
		d.prev, d.current = current, d.prev
		d.prevWidth = w
		d.prevHeight = h
		d.prevCursorX = pos.X
		d.prevCursorY = pos.Y
		d.prevCursorHid = cursorHidden
		d.prevAltScreen = isAlt
		return nil
	}

	// Swap buffers (avoids copy; prev becomes current for next call)
	d.prev, d.current = current, d.prev
	d.prevWidth = w
	d.prevHeight = h
	d.prevCursorX = pos.X
	d.prevCursorY = pos.Y
	d.prevCursorHid = cursorHidden
	d.prevAltScreen = isAlt

	// Return a copy of cells (the diffBuf will be reused next call)
	result := make([]DiffCell, len(cells))
	copy(result, cells)

	return &ScreenDiff{
		Cells:        result,
		CursorX:      pos.X,
		CursorY:      pos.Y,
		CursorHidden: cursorHidden,
		IsAltScreen:  isAlt,
		Width:        w,
		Height:       h,
	}
}

// ---- Per-subscriber signal ----
//
// The PTY read loop signals subscribers when the VT changes. The subscriber
// goroutine wakes, computes its own diff (via ScreenDiffer), and sends it.
// Multiple signals between wakes coalesce naturally (channel cap 1).

// DiffSignal is the per-subscriber state used by the daemon's diff streamer.
type DiffSignal struct {
	Signal chan struct{} // capacity 1, non-blocking wake
	Done   chan struct{} // closed when subscriber is removed
	Differ *ScreenDiffer
}

func NewDiffSignal() *DiffSignal {
	return &DiffSignal{
		Signal: make(chan struct{}, 1),
		Done:   make(chan struct{}),
		Differ: NewScreenDiffer(),
	}
}

// ---- Binary encoding/decoding ----
//
// Wire format (after the standard message header):
//   [2B width][2B height]
//   [2B cursorX][2B cursorY]
//   [1B flags: bit0=cursorHidden, bit1=altScreen, bits2-4=cursorStyle]
//   [2B titleLen][nB titleUTF8]
//   [4B numCells]
//   per cell:
//     [2B row][2B col][1B width]
//     [4B fg][4B bg][2B attrs]
//     [4B ulColor][1B ulStyle]
//     [2B contentLen][nB contentUTF8]

// EncodeScreenDiff serializes a ScreenDiff to binary.
func EncodeScreenDiff(ptyID string, diff *ScreenDiff) []byte {
	titleBytes := []byte(diff.Title)
	size := 36 + 2 + 2 + 2 + 2 + 1 + 2 + len(titleBytes) + 4
	for _, c := range diff.Cells {
		size += 22 + len(c.Content)
	}

	buf := make([]byte, size)
	off := 0

	copy(buf[off:off+36], ptyID)
	off += 36

	binary.BigEndian.PutUint16(buf[off:], uint16(diff.Width))
	off += 2
	binary.BigEndian.PutUint16(buf[off:], uint16(diff.Height))
	off += 2
	binary.BigEndian.PutUint16(buf[off:], uint16(diff.CursorX))
	off += 2
	binary.BigEndian.PutUint16(buf[off:], uint16(diff.CursorY))
	off += 2

	var flags uint8
	if diff.CursorHidden {
		flags |= 1
	}
	if diff.IsAltScreen {
		flags |= 2
	}
	flags |= uint8(diff.CursorStyle&0x7) << 2
	buf[off] = flags
	off++

	binary.BigEndian.PutUint16(buf[off:], uint16(len(titleBytes)))
	off += 2
	copy(buf[off:], titleBytes)
	off += len(titleBytes)

	binary.BigEndian.PutUint32(buf[off:], uint32(len(diff.Cells)))
	off += 4
	for _, c := range diff.Cells {
		binary.BigEndian.PutUint16(buf[off:], uint16(c.Row))
		off += 2
		binary.BigEndian.PutUint16(buf[off:], uint16(c.Col))
		off += 2
		buf[off] = uint8(c.Width)
		off++
		binary.BigEndian.PutUint32(buf[off:], c.Fg)
		off += 4
		binary.BigEndian.PutUint32(buf[off:], c.Bg)
		off += 4
		binary.BigEndian.PutUint16(buf[off:], c.Attrs)
		off += 2
		binary.BigEndian.PutUint32(buf[off:], c.UlColor)
		off += 4
		buf[off] = c.UlStyle
		off++
		contentBytes := []byte(c.Content)
		binary.BigEndian.PutUint16(buf[off:], uint16(len(contentBytes)))
		off += 2
		copy(buf[off:], contentBytes)
		off += len(contentBytes)
	}

	return buf[:off]
}

// DecodeScreenDiff deserializes a binary-encoded screen diff.
func DecodeScreenDiff(data []byte) (ptyID string, diff *ScreenDiff, err error) {
	if len(data) < 36+2+2+2+2+1+2+4 {
		return "", nil, errTooShort
	}
	off := 0

	ptyID = trimNulls(string(data[off : off+36]))
	off += 36

	diff = &ScreenDiff{}
	diff.Width = int(binary.BigEndian.Uint16(data[off:]))
	off += 2
	diff.Height = int(binary.BigEndian.Uint16(data[off:]))
	off += 2
	diff.CursorX = int(binary.BigEndian.Uint16(data[off:]))
	off += 2
	diff.CursorY = int(binary.BigEndian.Uint16(data[off:]))
	off += 2

	flags := data[off]
	off++
	diff.CursorHidden = flags&1 != 0
	diff.IsAltScreen = flags&2 != 0
	diff.CursorStyle = vt.CursorStyle((flags >> 2) & 0x7)

	titleLen := int(binary.BigEndian.Uint16(data[off:]))
	off += 2
	if off+titleLen > len(data) {
		return "", nil, errTooShort
	}
	diff.Title = string(data[off : off+titleLen])
	off += titleLen

	if off+4 > len(data) {
		return "", nil, errTooShort
	}
	numCells := int(binary.BigEndian.Uint32(data[off:]))
	off += 4

	diff.Cells = make([]DiffCell, numCells)
	for i := range numCells {
		if off+22 > len(data) {
			return "", nil, errTooShort
		}
		c := &diff.Cells[i]
		c.Row = int(binary.BigEndian.Uint16(data[off:]))
		off += 2
		c.Col = int(binary.BigEndian.Uint16(data[off:]))
		off += 2
		c.Width = int(data[off])
		off++
		c.Fg = binary.BigEndian.Uint32(data[off:])
		off += 4
		c.Bg = binary.BigEndian.Uint32(data[off:])
		off += 4
		c.Attrs = binary.BigEndian.Uint16(data[off:])
		off += 2
		c.UlColor = binary.BigEndian.Uint32(data[off:])
		off += 4
		c.UlStyle = data[off]
		off++
		if off+2 > len(data) {
			return "", nil, errTooShort
		}
		contentLen := int(binary.BigEndian.Uint16(data[off:]))
		off += 2
		if off+contentLen > len(data) {
			return "", nil, errTooShort
		}
		c.Content = string(data[off : off+contentLen])
		off += contentLen
	}

	return ptyID, diff, nil
}

func trimNulls(s string) string {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] != 0 {
			return s[:i+1]
		}
	}
	return ""
}

var errTooShort = &shortError{}

type shortError struct{}

func (e *shortError) Error() string { return "screen diff: data too short" }

// WriteScreenDiff writes a screen diff in the optimized binary format
// directly to a writer, using the standard message framing.
func WriteScreenDiff(w interface{ Write([]byte) (int, error) }, ptyID string, diff *ScreenDiff) error {
	payload := EncodeScreenDiff(ptyID, diff)
	totalLen := uint32(2 + len(payload))

	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], totalLen)
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	if _, err := w.Write([]byte{byte(MsgScreenDiff), byte(CodecGob)}); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// ---- Legacy types kept for compatibility during migration ----

// DirtyAccumulator is kept for the PTY.SubscribeScreenDiffs API but is
// no longer used in the hot path. The ScreenDiffer in the subscriber
// goroutine handles everything.
type DirtyAccumulator struct {
	mu       sync.Mutex
	hasDirty atomic.Bool
}

func NewDirtyAccumulator() *DirtyAccumulator {
	return &DirtyAccumulator{}
}
