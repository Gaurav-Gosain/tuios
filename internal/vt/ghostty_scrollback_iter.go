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

// CopyScrollback implements Terminal.CopyScrollback. The lines are read and
// encoded into a ring of their own, which costs what the reads cost, so here
// the copy does not shorten the time the lock is held; it keeps the reader's
// code the same for both backends.
func (t *GhosttyTerminal) CopyScrollback(from, end int) *ScrollbackCopy {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushRestoreLocked()
	src, from, end := t.historyRangeLocked(from, end)
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

// ScrollbackGeneration is a number that changes whenever the history may
// have. The library does not say when its history changes, so this is the
// count of writes, which changes more often than it has to.
func (t *GhosttyTerminal) ScrollbackGeneration() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.scrollGeneration
}
