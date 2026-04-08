// Package ghostty daemon integration. This file provides the bridge between
// the ghostty terminal (libghostty-vt) and tuios's daemon session protocol.
// Instead of streaming raw bytes to clients, the daemon uses ghostty's
// render state API to compute screen diffs and send only changed cells.
package ghostty

import (
	"encoding/binary"
	"sync"
)

// DaemonTerminal wraps a ghostty Terminal for use in daemon mode.
// It processes PTY output through the ghostty VT parser and provides
// event-driven screen diffs via the render state API.
type DaemonTerminal struct {
	mu       sync.Mutex
	term     *Terminal
	width    int
	height   int
	dirty    chan struct{} // signal channel (cap 1) for event-driven diffs
	writeCh  chan []byte   // async write channel for non-blocking writes
}

// NewDaemonTerminal creates a ghostty-backed terminal for a daemon PTY.
func NewDaemonTerminal(cols, rows int) (*DaemonTerminal, error) {
	term, err := NewTerminal(cols, rows)
	if err != nil {
		return nil, err
	}
	dt := &DaemonTerminal{
		term:    term,
		width:   cols,
		height:  rows,
		dirty:   make(chan struct{}, 1),
		writeCh: make(chan []byte, 256),
	}
	go dt.writerLoop()
	return dt, nil
}

// Free releases the terminal.
func (dt *DaemonTerminal) Free() {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	if dt.term != nil {
		dt.term.Free()
		dt.term = nil
	}
}

// writeChan is used by WriteNonBlocking to feed data asynchronously.
var _ = (*DaemonTerminal)(nil) // type check

// Write processes PTY output through the ghostty VT and signals dirty.
func (dt *DaemonTerminal) Write(data []byte) {
	dt.mu.Lock()
	if dt.term != nil {
		dt.term.Write(data)
	}
	dt.mu.Unlock()

	select {
	case dt.dirty <- struct{}{}:
	default:
	}
}

// WriteNonBlocking sends data to a channel for async processing.
// Drops if the channel is full (ghostty will catch up on next write).
func (dt *DaemonTerminal) WriteNonBlocking(data []byte) {
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)
	select {
	case dt.writeCh <- dataCopy:
	default:
	}
}

// writerLoop drains the write channel and feeds data to ghostty.
func (dt *DaemonTerminal) writerLoop() {
	for data := range dt.writeCh {
		func() {
			defer func() {
				if r := recover(); r != nil {
					// CGo panic - ghostty VT issue. Don't crash the daemon.
				}
			}()
			dt.Write(data)
		}()
	}
}

// Resize changes the terminal dimensions.
func (dt *DaemonTerminal) Resize(cols, rows, cellW, cellH int) error {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	dt.width = cols
	dt.height = rows
	if dt.term != nil {
		return dt.term.Resize(cols, rows, cellW, cellH)
	}
	return nil
}

// DirtySignal returns the channel that signals when new content is available.
func (dt *DaemonTerminal) DirtySignal() <-chan struct{} {
	return dt.dirty
}

// ReadDiff reads dirty rows from the render state under the lock.
// Returns nil if nothing changed since the last read.
func (dt *DaemonTerminal) ReadDiff() *ScreenDiff {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	if dt.term == nil {
		return nil
	}
	return dt.term.ReadDirtyRows()
}

// EncodeDiff serializes a ScreenDiff to a compact binary format for
// transmission to clients. Format:
//
//	[2B cols][2B rows][2B cursorX][2B cursorY][1B cursorVisible]
//	[1B fullRedraw][2B numDirtyRows]
//	per dirty row:
//	  [2B y][2B numCells]
//	  per cell:
//	    [2B contentLen][nB contentUTF8]
//	    [1B fgSet][3B fgRGB][1B bgSet][3B bgRGB]
//	    [1B flags: bold|italic|underline|strikethrough|wide]
func EncodeDiff(diff *ScreenDiff) []byte {
	if diff == nil {
		return nil
	}

	// Estimate size
	size := 11 // header
	for _, row := range diff.Rows {
		size += 4 // y + numCells
		for _, cell := range row.Cells {
			size += 2 + len(cell.Content) + 9 // content + colors + flags
		}
	}

	buf := make([]byte, size)
	off := 0

	binary.BigEndian.PutUint16(buf[off:], uint16(diff.Cols))
	off += 2
	binary.BigEndian.PutUint16(buf[off:], uint16(diff.RowCount))
	off += 2
	binary.BigEndian.PutUint16(buf[off:], uint16(diff.Cursor.X))
	off += 2
	binary.BigEndian.PutUint16(buf[off:], uint16(diff.Cursor.Y))
	off += 2
	if diff.Cursor.Visible {
		buf[off] = 1
	}
	off++
	if diff.FullRedraw {
		buf[off] = 1
	}
	off++
	binary.BigEndian.PutUint16(buf[off:], uint16(len(diff.Rows)))
	off += 2

	for _, row := range diff.Rows {
		binary.BigEndian.PutUint16(buf[off:], uint16(row.Y))
		off += 2
		binary.BigEndian.PutUint16(buf[off:], uint16(len(row.Cells)))
		off += 2

		for _, cell := range row.Cells {
			content := []byte(cell.Content)
			binary.BigEndian.PutUint16(buf[off:], uint16(len(content)))
			off += 2
			copy(buf[off:], content)
			off += len(content)

			if cell.FgSet {
				buf[off] = 1
			}
			off++
			buf[off] = cell.FgR
			buf[off+1] = cell.FgG
			buf[off+2] = cell.FgB
			off += 3

			if cell.BgSet {
				buf[off] = 1
			}
			off++
			buf[off] = cell.BgR
			buf[off+1] = cell.BgG
			buf[off+2] = cell.BgB
			off += 3

			var flags uint8
			if cell.Bold {
				flags |= 1
			}
			if cell.Italic {
				flags |= 2
			}
			if cell.Underline {
				flags |= 4
			}
			if cell.Strikethrough {
				flags |= 8
			}
			if cell.Wide {
				flags |= 16
			}
			buf[off] = flags
			off++
		}
	}

	return buf[:off]
}
