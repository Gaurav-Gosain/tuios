//go:build ghostty

package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
	gh "go.mitchellh.com/libghostty"
)

// The bulk history readers of Terminal. The library keeps history in its own
// form, so each line is read through readHistoryLineLocked as ScrollbackLine
// reads it, but not kept in the line cache: a walk of the whole history would
// otherwise churn the cache and leave it holding the last lines walked.

// historySourceLocked is the terminal that holds the main screen's history:
// the live one, or while the alternate screen is up the decoded copy switched
// back to the main screen. It returns nil when there is none.
func (t *GhosttyTerminal) historySourceLocked() *gh.Terminal {
	if t.closed.Load() {
		return nil
	}
	if t.activeAltLiveLocked() {
		return t.altHistoryLocked()
	}
	return t.term
}

// historyRangeLocked clamps [from, end) to the history and returns the
// terminal to read it from, or nil when there is nothing to read.
func (t *GhosttyTerminal) historyRangeLocked(from, end int) (*gh.Terminal, int, int) {
	from, end = max(from, 0), min(end, t.scrollbackLenLocked())
	if from >= end {
		return nil, from, end
	}
	return t.historySourceLocked(), from, end
}

// ScrollbackRows implements Terminal.ScrollbackRows. fn runs under the
// terminal's lock.
func (t *GhosttyTerminal) ScrollbackRows(from, end int, fn func(index int, line uv.Line) bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushRestoreLocked()
	src, from, end := t.historyRangeLocked(from, end)
	if src == nil {
		return
	}
	for i := from; i < end; i++ {
		if !fn(i, t.readHistoryLineLocked(src, i)) {
			return
		}
	}
}

// ScrollbackText implements Terminal.ScrollbackText. fn runs under the
// terminal's lock.
func (t *GhosttyTerminal) ScrollbackText(from, end int, fn func(index, width int, cells []TextCell) bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushRestoreLocked()
	src, from, end := t.historyRangeLocked(from, end)
	if src == nil {
		return
	}
	var cells []TextCell
	var text []byte
	var ends []int
	for i := from; i < end; i++ {
		line := t.readHistoryLineLocked(src, i)
		// The contents go into one buffer per line, and the cells are cut
		// from it once it has stopped growing.
		text, ends = text[:0], ends[:0]
		for x := range line {
			text = append(text, line[x].Content...)
			ends = append(ends, len(text))
		}
		cells = cells[:0]
		at := 0
		for x := range line {
			cells = append(cells, TextCell{Content: text[at:ends[x]:ends[x]], Width: line[x].Width})
			at = ends[x]
		}
		if !fn(i, len(line), cells) {
			return
		}
	}
}

// CopyScrollback implements Terminal.CopyScrollback. The library has no copy
// of its lines that is cheap to take, and reading them one cell at a time
// cost about 50 ms and 40 MB for 1000 rows of 200 columns, all under the
// lock. So under the lock the library writes a snapshot of the terminal, a
// few hundred bytes a row, and the palette is copied. The lines are read out
// of the decoded snapshot on first use of the copy, after the caller let go
// of the lock.
func (t *GhosttyTerminal) CopyScrollback(from, end int) *ScrollbackCopy {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushRestoreLocked()
	if t.closed.Load() {
		return &ScrollbackCopy{}
	}
	from, end = max(from, 0), min(end, t.scrollbackLenLocked())
	if from >= end {
		return &ScrollbackCopy{}
	}
	data, err := t.term.Snapshot()
	if err != nil || len(data) == 0 {
		return t.copyScrollbackLocked(from, end)
	}
	alt := t.activeAltLiveLocked()
	width, pal := t.width, t.paletteLocked()
	return lazyScrollbackCopy(func() *Scrollback {
		src := decodeHistorySnapshot(data, alt)
		if src == nil {
			return nil
		}
		defer src.Close()
		c := &ScrollbackCopy{}
		for i := from; i < end; i++ {
			c.push(t.readHistoryLine(src, i, width, pal),
				wrapFlag(ghosttyRowWrap(src, gh.Point{Tag: gh.PointTagHistory, Y: uint32(i)})), end-from)
		}
		return c.sb
	})
}

// copyScrollbackLocked reads the lines one by one under the lock, for when the
// library writes no snapshot.
func (t *GhosttyTerminal) copyScrollbackLocked(from, end int) *ScrollbackCopy {
	src := t.historySourceLocked()
	if src == nil {
		return &ScrollbackCopy{}
	}
	c := &ScrollbackCopy{}
	for i := from; i < end; i++ {
		c.push(t.readHistoryLineLocked(src, i),
			wrapFlag(ghosttyRowWrap(src, gh.Point{Tag: gh.PointTagHistory, Y: uint32(i)})), end-from)
	}
	return c
}

// decodeHistorySnapshot makes a terminal of its own from a snapshot. When the
// alternate screen was active, the copy is switched back to the main screen,
// whose history is the one every reader wants (see altHistoryLocked). It
// returns nil when the snapshot does not decode. The caller closes it.
func decodeHistorySnapshot(data []byte, alt bool) *gh.Terminal {
	dec, err := gh.NewSnapshotDecoderBytes(data)
	if err != nil {
		return nil
	}
	defer dec.Close()
	src, err := dec.Decode()
	if err != nil || src == nil {
		return nil
	}
	if alt {
		src.VTWrite([]byte("\x1b[?1049l\x1b[?1047l"))
	}
	return src
}

// ScrollbackGeneration is a number that changes whenever the history may
// have. The library does not say when its history changes, so this is the
// count of writes, which changes more often than it has to.
func (t *GhosttyTerminal) ScrollbackGeneration() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.scrollGeneration
}
