//go:build ghostty

package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
	gh "go.mitchellh.com/libghostty"
)

// The state a snapshot carries beyond the cells, the cursor and the DEC
// modes, on the library: the tab stops, the titles, the colours the guest
// set, the ANSI modes, and the input the parser is part way through.
//
// The library exposes the title, the ANSI modes and its unfinished input. It
// keeps the tab stops where no query reaches them and does not keep the icon
// name or a title stack at all, so those are read off the stream as it goes
// past. The guest's colours never reach the library (handleColorOSC). The
// restore puts each back the way a guest would set it.

// cursorXLocked is the library's cursor column, after the scanner has handed
// it everything before the sequence being observed.
func (t *GhosttyTerminal) cursorXLocked() (int, bool) {
	if t.closed.Load() {
		return 0, false
	}
	t.scanner.flushOut()
	x, err := t.term.CursorX()
	if err != nil {
		return 0, false
	}
	return int(x), true
}

// setTabStopLocked follows HTS, and CTC 0: a stop at the cursor.
func (t *GhosttyTerminal) setTabStopLocked() {
	if x, ok := t.cursorXLocked(); ok && x < t.tabstops.Width() {
		t.tabstops.Set(x)
	}
}

// clearTabStopLocked follows TBC 0 and CTC 2: no stop at the cursor.
func (t *GhosttyTerminal) clearTabStopLocked() {
	if x, ok := t.cursorXLocked(); ok && x < t.tabstops.Width() {
		t.tabstops.Reset(x)
	}
}

// observeTabControl follows the CSI forms that change the tab stops, as the
// library reads them: TBC (CSI g) with 0 clears the stop at the cursor and
// with 3 clears them all, and with no parameter does nothing, where xterm
// reads it as 0; CTC (CSI W) with no parameter or 0
// sets one at the cursor, with 2 clears it and with 5 clears them all; and
// DECST8C (CSI ? 5 W) puts the default table back. Anything else changes
// nothing in the library either.
func (t *GhosttyTerminal) observeTabControl(prefix, final byte, params []byte) {
	n := csiParamCount(params)
	v, _ := csiFirstParam(params)
	switch {
	case final == 'g' && prefix == 0 && n == 1:
		switch v {
		case 0:
			t.clearTabStopLocked()
		case 3:
			t.tabstops.Clear()
		}
	case final == 'W' && prefix == 0 && n <= 1:
		switch v {
		case 0:
			t.setTabStopLocked()
		case 2:
			t.clearTabStopLocked()
		case 5:
			t.tabstops.Clear()
		}
	case final == 'W' && prefix == '?' && n == 1 && v == 5:
		t.tabstops = uv.DefaultTabStops(t.width)
	}
}

// observeTitleStack implements XTWINOPS 22 and 23, which the library reads
// and does nothing with. 22 saves the title, the icon name or both, as its
// second parameter says, and 23 puts back what the last entry saved. A title
// put back reaches the library as an OSC 2 sent once the CSI has ended, so
// the library reports it as it reports any other.
func (t *GhosttyTerminal) observeTitleStack(params []byte) {
	cmd, which := csiTwoParams(params, 0, 0)
	if which < 0 || which > 2 {
		return
	}
	switch cmd {
	case 22:
		title := ""
		if !t.closed.Load() {
			t.scanner.flushOut()
			title, _ = t.term.Title()
		}
		entry := savedTitle{
			icon: t.iconName, hasIcon: which != 2,
			title: title, hasTitle: which != 1,
		}
		if len(t.titleStack) >= maxTitleStack {
			t.titleStack = append(t.titleStack[:0], t.titleStack[1:]...)
		}
		t.titleStack = append(t.titleStack, entry)
	case 23:
		if len(t.titleStack) == 0 {
			return
		}
		entry := t.titleStack[len(t.titleStack)-1]
		t.titleStack = t.titleStack[:len(t.titleStack)-1]
		if entry.hasIcon && which != 2 {
			t.iconName = entry.icon
			icon := entry.icon
			t.queue(func(cb Callbacks) {
				if cb.IconName != nil {
					cb.IconName(icon)
				}
			})
		}
		if entry.hasTitle && which != 1 {
			title := entry.title
			t.scanner.afterCSI = func() { t.sendTitleLocked(title) }
		}
	}
}

// sendTitleLocked sets the library's title the way a guest does.
func (t *GhosttyTerminal) sendTitleLocked(title string) {
	if t.closed.Load() {
		return
	}
	t.term.VTWrite([]byte("\x1b]2;" + title + "\x1b\\"))
}

// TabStops lists the columns that hold a tab stop.
func (t *GhosttyTerminal) TabStops() []int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushRestoreLocked()
	stops := []int{}
	for x := range t.tabstops.Width() {
		if t.tabstops.IsStop(x) {
			stops = append(stops, x)
		}
	}
	return stops
}

// RestoreTabStops replaces the tab stops. The restore clears the library's
// table and sets each stop with HTS.
func (t *GhosttyTerminal) RestoreTabStops(cols []int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.pendingRestore()
	r.tabStops = append([]int{}, cols...)
	r.hasTabStops = true
}

// Titles is the window title, the icon name and the title stack.
func (t *GhosttyTerminal) Titles() Titles {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushRestoreLocked()
	var out Titles
	if !t.closed.Load() {
		out.Title, _ = t.term.Title()
	}
	out.Icon = t.iconName
	for _, s := range t.titleStack {
		out.Stack = append(out.Stack, TitleEntry{Title: s.title, Icon: s.icon, HasTitle: s.hasTitle, HasIcon: s.hasIcon})
	}
	return out
}

// RestoreTitles puts back the titles and the newest entries of the stack.
func (t *GhosttyTerminal) RestoreTitles(ti Titles) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.pendingRestore()
	ti.Stack = append([]TitleEntry(nil), ti.Stack[max(len(ti.Stack)-maxTitleStack, 0):]...)
	r.titles = &ti
}

// GuestColors is what the guest set with OSC 4, 10, 11 and 12.
func (t *GhosttyTerminal) GuestColors() GuestColors {
	t.mu.Lock()
	defer t.mu.Unlock()
	return GuestColors{Palette: t.colors, Fg: t.guestFg, Bg: t.guestBg, Cursor: t.guestCur}
}

// RestoreGuestColors replaces what the guest set with OSC 4, 10, 11 and 12.
// They are kept here and never in the library, so they apply at once.
func (t *GhosttyTerminal) RestoreGuestColors(c GuestColors) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.colors = c.Palette
	t.guestFg, t.guestBg, t.guestCur = c.Fg, c.Bg, c.Cursor
	t.refreshPaletteClaimsLocked()
	// A palette entry changes how every cell that names it is drawn.
	t.styleCache = make(map[uint16]uv.Style)
	t.scrollCache = make(map[int]uv.Line)
	t.scrollCacheGen = 0
	t.markAllDirtyLocked()
}

// ghosttyANSIModes are the ANSI modes ANSIModes reports, the ones the pure
// emulator implements too.
var ghosttyANSIModes = []struct {
	num  int
	mode gh.Mode
}{
	{4, gh.ModeInsert},
	{20, gh.ModeLinefeed},
}

// ANSIModes reports insert mode (4) and newline mode (20).
func (t *GhosttyTerminal) ANSIModes() map[int]bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushRestoreLocked()
	modes := make(map[int]bool, len(ghosttyANSIModes))
	if t.closed.Load() {
		return modes
	}
	for _, m := range ghosttyANSIModes {
		if v, err := t.term.Mode(m.mode); err == nil {
			modes[m.num] = v
		}
	}
	return modes
}

// RestoreANSIModes sets insert mode and newline mode. The restore sends them
// last, after everything it prints.
func (t *GhosttyTerminal) RestoreANSIModes(modes map[int]bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.pendingRestore()
	if r.ansiModes == nil {
		r.ansiModes = make(map[int]bool, len(ghosttyANSIModes))
	}
	for _, m := range ghosttyANSIModes {
		if v, ok := modes[m.num]; ok {
			r.ansiModes[m.num] = v
		}
	}
}

// PendingInput is the unfinished input: what the library holds of a sequence
// or a character it has started, followed by what the scanner holds back
// from it. The scanner holds only the tail of the stream, so the two join in
// order.
func (t *GhosttyTerminal) PendingInput() []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushRestoreLocked()
	if t.closed.Load() {
		return nil
	}
	var out []byte
	if cont, err := t.term.Continuation(); err == nil {
		out = cont
	}
	out = t.scanner.appendHeld(out)
	if len(out) > MaxPendingInput {
		out = out[:MaxPendingInput]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// RestorePendingInput drops the unfinished input the library and the scanner
// hold, applies a pending restore, and reads p.
//
// The library lets go of a sequence at CAN. It lets go of a UTF-8 character
// only by ending it, which prints U+FFFD at the cursor; ApplyTerminalState
// calls this after it has buffered a restore, which paints the screen over
// it.
func (t *GhosttyTerminal) RestorePendingInput(p []byte) {
	t.mu.Lock()
	if t.closed.Load() {
		t.mu.Unlock()
		return
	}
	if ground, err := t.term.VTGround(); err == nil && !ground {
		t.term.VTWrite([]byte{0x18})
	}
	t.scanner.reset()
	t.scanner.afterCSI = nil
	t.flushRestoreLocked()
	if len(p) > MaxPendingInput {
		p = p[:MaxPendingInput]
	}
	t.scanner.Scan(p)
	t.gridStale = true
	t.scrollGeneration++
	t.refreshCachesLocked()
	q := t.takeQueue()
	t.mu.Unlock()
	t.drain(q)
}

// appendHeld appends to out the bytes the scanner has read and not yet handed
// to the sink: the introducer and payload of a string it withholds, an ESC
// whose meaning the next byte decides, or the prefix of a DCS before its
// final byte. Fed to a fresh scanner, they leave it in the state this one is
// in. A payload past ghosttyScanCap gets one byte more than the cap, so the
// fresh scanner overflows too.
func (s *ghosttyScanner) appendHeld(out []byte) []byte {
	payload := func(intro ...byte) {
		out = append(out, intro...)
		out = append(out, s.seq...)
		if s.overflow && len(s.seq) > 0 {
			out = append(out, s.seq[len(s.seq)-1])
		}
	}
	switch s.state {
	case gsEsc:
		out = append(out, 0x1b)
	case gsEscInter:
		out = append(out, 0x1b, s.escInter)
	case gsOsc:
		payload(0x1b, ']')
	case gsOscEsc:
		payload(0x1b, ']')
		out = append(out, 0x1b)
	case gsDcs:
		out = append(out, 0x1b, 'P')
		out = append(out, s.dcsParams...)
	case gsSixel, gsSixelEsc:
		intro := append([]byte{0x1b, 'P'}, s.dcsParams...)
		payload(append(intro, 'q')...)
		if s.state == gsSixelEsc {
			out = append(out, 0x1b)
		}
	case gsApc:
		payload(0x1b, '_')
	case gsApcEsc:
		payload(0x1b, '_')
		out = append(out, 0x1b)
	case gsDcsBodyEsc, gsStringEsc:
		// The body went to the sink as it came; only the ESC is held.
		out = append(out, 0x1b)
	}
	return out
}

// reset drops the sequence the scanner is in and puts it at ground, for a
// restore that replaces the stream it was reading.
func (s *ghosttyScanner) reset() {
	s.state = gsGround
	s.out = s.out[:0]
	s.resetSeq()
	s.escInter = 0
	s.csiPrefix, s.csiInter = 0, 0
	s.dcsParams = s.dcsParams[:0]
	s.u8n, s.u8want = 0, 0
}
