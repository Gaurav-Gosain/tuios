//go:build ghostty

package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
	gh "go.mitchellh.com/libghostty"
)

func (t *GhosttyTerminal) activeAltLiveLocked() bool {
	if t.closed.Load() {
		return false
	}
	legacy, _ := t.term.Mode(gh.ModeAltScreenLegacy)
	a, _ := t.term.Mode(gh.ModeAltScreen)
	b, _ := t.term.Mode(gh.ModeAltScreenSaveCursorClearEnter)
	return legacy || a || b
}

func (t *GhosttyTerminal) scrollbackLenLocked() int {
	if t.closed.Load() {
		return 0
	}
	if t.activeAltLiveLocked() {
		return t.mainSbLen
	}
	n, err := t.term.ScrollbackRows()
	if err != nil {
		return t.mainSbLen
	}
	t.mainSbLen = int(n)
	return t.mainSbLen
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

const ghosttyScrollCacheCap = 256

// ScrollbackLine implements [Terminal.ScrollbackLine].
func (t *GhosttyTerminal) ScrollbackLine(index int) uv.Line {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushRestoreLocked()
	return t.scrollbackLineLocked(index)
}

func (t *GhosttyTerminal) scrollbackLineLocked(index int) uv.Line {
	if t.closed.Load() || index < 0 || index >= t.scrollbackLenLocked() {
		return nil
	}
	limit := min(ghosttyScrollCacheCap, max(1, cacheCellCap/max(t.width, 1)))
	if t.scrollCacheGen != t.scrollGeneration || len(t.scrollCache) >= limit {
		clear(t.scrollCache)
		t.scrollCacheGen = t.scrollGeneration
	}
	if line, ok := t.scrollCache[index]; ok {
		return line
	}
	src := t.term
	if t.activeAltLiveLocked() {
		src = t.altHistoryLocked()
		if src == nil {
			return nil
		}
	}
	line := t.readHistoryLineLocked(src, index)
	t.scrollCache[index] = line
	return line
}

func (t *GhosttyTerminal) readHistoryLineLocked(src *gh.Terminal, index int) uv.Line {
	return t.readHistoryLine(src, index, t.width, nil)
}

func (t *GhosttyTerminal) readHistoryLine(src *gh.Terminal, index, width int, pal *ghosttyPalette) uv.Line {
	line := make(uv.Line, width)
	var bg backgroundMemo
	for x := 0; x < width; x++ {
		line[x] = uv.Cell{Content: " ", Width: 1}
		ref, err := src.GridRef(gh.Point{Tag: gh.PointTagHistory, X: uint16(x), Y: uint32(index)})
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
		switch dc.tag {
		case gh.CellContentCodepoint:
			if dc.cp != 0 {
				out.Content = string(dc.cp)
			} else {
				out.Content = " "
				out.Width = 1
			}
		case gh.CellContentCodepointGrapheme:
			out.Content = string(dc.cp)
			if cps, err := ref.Graphemes(); err == nil && len(cps) > 0 {
				b := make([]rune, 0, len(cps))
				for _, cp := range cps {
					b = append(b, rune(cp))
				}
				out.Content = string(b)
			}
		default:
			out.Content = " "
			out.Width = 1
			out.Style = t.backgroundOnlyStyle(&bg, cell, dc.tag, pal)
		}
		if dc.styleID != 0 {
			if gs, err := ref.Style(); err == nil && gs != nil {
				out.Style = t.convertStyleIn(gs, pal)
			}
		}
		if dc.link {
			if uri, err := ref.HyperlinkURI(); err == nil && uri != "" {
				out.Link = uv.Link{URL: LinkURI(uri)}
			}
		}
		line[x] = out
	}
	return line
}

func (t *GhosttyTerminal) altHistoryLocked() *gh.Terminal {
	if t.altHistory != nil && t.altHistoryGen == t.scrollGeneration {
		return t.altHistory
	}
	t.dropAltHistoryLocked()
	data, err := t.term.Snapshot()
	if err != nil || len(data) == 0 {
		return nil
	}
	dec, err := gh.NewSnapshotDecoderBytes(data)
	if err != nil {
		return nil
	}
	defer dec.Close()
	restored, err := dec.Decode()
	if err != nil || restored == nil {
		return nil
	}
	restored.VTWrite([]byte("\x1b[?1049l\x1b[?1047l"))
	t.altHistory = restored
	t.altHistoryGen = t.scrollGeneration
	return t.altHistory
}

func (t *GhosttyTerminal) dropAltHistoryLocked() {
	if t.altHistory != nil {
		t.altHistory.Close()
		t.altHistory = nil
	}
}

// ClearScrollback drops the main screen's history. ED 3 addresses the active
// screen, so while the alternate screen is up the clear is deferred to the
// next return to the main screen; the count reads as cleared immediately,
// which is what the pure emulator's direct ring clear reports.
func (t *GhosttyTerminal) ClearScrollback() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed.Load() {
		return
	}
	t.flushRestoreLocked()
	if t.activeAltLiveLocked() {
		t.mainSbLen = 0
		t.pendingMainSbClear = true
	} else {
		t.term.VTWrite([]byte("\x1b[3J"))
		t.mainSbLen = 0
	}
	t.scrollGeneration++
	t.dropAltHistoryLocked()
	if t.semanticMarkers != nil {
		t.semanticMarkers.RemoveOnScreen(0)
	}
}

// SetScrollbackMaxLines records the limit. The library takes the limit at
// construction; a runtime change applies from the next terminal, which the
// differential harness documents as an accepted divergence.
// SetScrollbackMaxLines records the depth asked for. libghostty takes the
// depth only when the terminal is made, so this cannot change what the
// library keeps; build a pane through NewWithScrollback to set it.
func (t *GhosttyTerminal) SetScrollbackMaxLines(maxLines int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.scrollbackMax = maxLines
}

// PushScrollbackLine implements [Terminal.PushScrollbackLine].
func (t *GhosttyTerminal) PushScrollbackLine(line uv.Line) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.pendingRestore()
	r.scrollback = append(r.scrollback, line)
}

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

func (r ghosttyLockedReader) lineSoftWrapped(absLine int) bool {
	if sbLen := r.t.scrollbackLenLocked(); absLine < sbLen {
		wrapped, _ := r.t.scrollbackSoftWrappedLocked(absLine)
		return wrapped
	} else {
		wrapped, _ := r.t.rowSoftWrappedLocked(absLine - sbLen)
		return wrapped
	}
}

func (t *GhosttyTerminal) readerNoLock() markerGridReader { return ghosttyLockedReader{t} }

type ghosttyMarkReader struct{ t *GhosttyTerminal }

func (r ghosttyMarkReader) Width() int                       { return r.t.width }
func (r ghosttyMarkReader) Height() int                      { return r.t.height }
func (r ghosttyMarkReader) ScrollbackLen() int               { return r.t.scrollbackLenLocked() }
func (r ghosttyMarkReader) ScrollbackLine(index int) uv.Line { return r.t.scrollbackLineLocked(index) }
func (r ghosttyMarkReader) lineSoftWrapped(absLine int) bool {
	return ghosttyLockedReader(r).lineSoftWrapped(absLine)
}

func (r ghosttyMarkReader) CellAt(x, y int) *uv.Cell {
	t := r.t
	if t.closed.Load() || x < 0 || y < 0 || x >= t.width || y >= t.height {
		return nil
	}
	ref, err := t.term.GridRef(gh.Point{Tag: gh.PointTagActive, X: uint16(x), Y: uint32(y)})
	if err != nil || ref == nil {
		return nil
	}
	cell, err := ref.Cell()
	if err != nil || cell == nil {
		return nil
	}
	var dc decodedCell
	if t.dec.ok {
		dc = t.dec.decode(cell.PackedValue())
	} else {
		dc = decodeCellSlow(cell)
	}
	switch dc.wide {
	case gh.CellWideSpacerTail:
		return &uv.Cell{}
	case gh.CellWideSpacerHead:
		return &uv.Cell{Content: " ", Width: 1}
	}
	out := &uv.Cell{Width: 1}
	if dc.wide == gh.CellWideWide {
		out.Width = 2
	}
	switch dc.tag {
	case gh.CellContentCodepoint:
		out.Content = " "
		if dc.cp != 0 {
			out.Content = string(dc.cp)
		} else {
			out.Width = 1
		}
	case gh.CellContentCodepointGrapheme:
		out.Content = string(dc.cp)
		if cps, err := ref.Graphemes(); err == nil && len(cps) > 0 {
			b := make([]rune, 0, len(cps))
			for _, cp := range cps {
				b = append(b, rune(cp))
			}
			out.Content = string(b)
		}
	default:
		out.Content = " "
		out.Width = 1
	}
	return out
}
