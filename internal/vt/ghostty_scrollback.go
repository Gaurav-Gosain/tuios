//go:build ghostty

package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
	gh "go.mitchellh.com/libghostty"
)

// Scrollback is the MAIN screen's history, whichever screen is active: every
// accessor on the pure emulator reads scrs[0], and consumers lean on that.
// The app computes kitty-placement absolute lines as ScrollbackLen()+cursorY
// while a full-screen guest owns the alternate screen, so an implementation
// that reported the alternate screen's (empty) history shifted every
// placement by the pane's real history and image previews vanished; wire
// snapshots taken mid-yazi lost the pane's history the same way.
//
// The library reads history from the active screen only, so two shadows
// bridge the gap: the count is cached whenever the main screen is active
// (the alternate screen cannot grow or shrink primary history), and line
// reads during alt go through a decoded snapshot whose private copy is
// switched back to the main screen.

// activeAltLiveLocked reports whether the alternate screen is active right
// now, from the library rather than the post-write cache: scanner hooks run
// mid-write, where the cache is one chunk stale.
func (t *GhosttyTerminal) activeAltLiveLocked() bool {
	if t.closed.Load() {
		return false
	}
	a, _ := t.term.Mode(gh.ModeAltScreen)
	b, _ := t.term.Mode(gh.ModeAltScreenSave)
	return a || b
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
	if t.scrollCacheGen != t.scrollGeneration {
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

// readHistoryLineLocked reads one history row as uv cells from src, which is
// either the live terminal (main screen active) or the decoded snapshot copy.
func (t *GhosttyTerminal) readHistoryLineLocked(src *gh.Terminal, index int) uv.Line {
	line := make(uv.Line, t.width)
	for x := 0; x < t.width; x++ {
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
	return line
}

// altHistoryLocked returns a decoded snapshot of the terminal with its copy
// switched to the main screen, so primary history is readable while the real
// terminal shows the alternate screen. Cached per write generation.
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
	// The copy replicated the alt-screen state; switching it back exposes
	// the primary screen's history. Only the copy is written to.
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
