//go:build ghostty

package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
	gh "go.mitchellh.com/libghostty"
)

// Scrollback lives in libghostty; lines are read back on demand through grid
// references into history and cached per write generation, since copy mode
// and search read the same lines many times between writes.

func (t *GhosttyTerminal) scrollbackLenLocked() int {
	n, err := t.term.ScrollbackRows()
	if err != nil {
		return 0
	}
	return int(n)
}

// ScrollbackLen deliberately does not flush a pending restore:
// ApplyTerminalState reads it between restore primitives, and a flush there
// would split the restore into two synthesized streams, the second of whose
// hard reset destroys the first. Pending lines count as pushed, which is
// what the pure emulator's incremental pushes report too.
func (t *GhosttyTerminal) ScrollbackLen() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := t.scrollbackLenLocked()
	if t.restore != nil {
		n += len(t.restore.scrollback)
	}
	return n
}

func (t *GhosttyTerminal) ScrollbackLine(index int) uv.Line {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushRestoreLocked()
	return t.scrollbackLineLocked(index)
}

func (t *GhosttyTerminal) scrollbackLineLocked(index int) uv.Line {
	if index < 0 || index >= t.scrollbackLenLocked() {
		return nil
	}
	if t.scrollCacheGen != t.scrollGeneration {
		clear(t.scrollCache)
		t.scrollCacheGen = t.scrollGeneration
	}
	if line, ok := t.scrollCache[index]; ok {
		return line
	}
	line := make(uv.Line, t.width)
	for x := 0; x < t.width; x++ {
		line[x] = uv.Cell{Content: " ", Width: 1}
		ref, err := t.term.GridRef(gh.Point{Tag: gh.PointTagHistory, X: uint16(x), Y: uint32(index)})
		if err != nil || ref == nil {
			continue
		}
		cell, err := ref.Cell()
		if err != nil || cell == nil {
			continue
		}
		var dc decodedCell
		if t.dec.ok {
			dc = t.dec.decode(cell.PackedValue())
		} else {
			dc = decodeCellSlow(cell)
		}
		switch dc.wide {
		case gh.CellWideSpacerTail, gh.CellWideSpacerHead:
			line[x] = uv.Cell{}
			continue
		}
		out := uv.Cell{Width: 1}
		if dc.wide == gh.CellWideWide {
			out.Width = 2
		}
		if dc.cp != 0 {
			out.Content = string(dc.cp)
			if dc.tag == gh.CellContentCodepointGrapheme {
				if cps, err := ref.Graphemes(); err == nil && len(cps) > 0 {
					b := make([]rune, 0, len(cps))
					for _, cp := range cps {
						b = append(b, rune(cp))
					}
					out.Content = string(b)
				}
			}
		} else {
			out.Content = " "
			out.Width = 1
		}
		if dc.styleID != 0 {
			if gs, err := ref.Style(); err == nil && gs != nil {
				out.Style = t.convertStyle(gs)
			}
		}
		if dc.link {
			if uri, err := ref.HyperlinkURI(); err == nil && uri != "" {
				out.Link = uv.Link{URL: uri}
			}
		}
		line[x] = out
	}
	t.scrollCache[index] = line
	return line
}

// ClearScrollback drops history by synthesizing ED 3, the sequence whose
// meaning is exactly this.
func (t *GhosttyTerminal) ClearScrollback() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushRestoreLocked()
	t.term.VTWrite([]byte("\x1b[3J"))
	t.scrollGeneration++
	if t.semanticMarkers != nil {
		t.semanticMarkers.RemoveOnScreen(0)
	}
}

// SetScrollbackMaxLines records the limit. The library takes the limit at
// construction; a runtime change applies from the next terminal, which the
// differential harness documents as an accepted divergence.
func (t *GhosttyTerminal) SetScrollbackMaxLines(maxLines int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.scrollbackMax = maxLines
}

func (t *GhosttyTerminal) PushScrollbackLine(line uv.Line) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.pendingRestore()
	r.scrollback = append(r.scrollback, line)
}

// ghosttyLockedReader gives extractCommandTextFrom a read surface that does
// not re-take the lock.
type ghosttyLockedReader struct{ t *GhosttyTerminal }

func (r ghosttyLockedReader) Width() int  { return r.t.width }
func (r ghosttyLockedReader) Height() int { return r.t.height }
func (r ghosttyLockedReader) ScrollbackLen() int {
	return r.t.scrollbackLenLocked()
}
func (r ghosttyLockedReader) ScrollbackLine(index int) uv.Line {
	return r.t.scrollbackLineLocked(index)
}
func (r ghosttyLockedReader) CellAt(x, y int) *uv.Cell {
	r.t.syncLocked()
	return r.t.bufs[r.t.active].CellAt(x, y)
}

func (t *GhosttyTerminal) readerNoLock() markerGridReader { return ghosttyLockedReader{t} }
