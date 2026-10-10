package vt

import (
	"image/color"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/parser"

	uv "github.com/charmbracelet/ultraviolet"
)

// The state a snapshot carries beyond the cells, the cursor and the DEC
// modes, on the pure emulator: the tab stops, the titles, the colours the
// guest set, the ANSI modes, and the input the parser is part way through.
// Each decides what output that has not arrived yet does, so a client restored
// without it diverges at the next byte that reads it.

// TabStops lists the columns that hold a tab stop.
func (e *Emulator) TabStops() []int {
	stops := []int{}
	for x := range e.tabstops.Width() {
		if e.tabstops.IsStop(x) {
			stops = append(stops, x)
		}
	}
	return stops
}

// RestoreTabStops replaces the tab stops with cols. A column off the screen
// is dropped.
func (e *Emulator) RestoreTabStops(cols []int) {
	e.tabstops = tabStopsAt(e.Width(), cols)
}

// tabStopsAt is a table of width columns with a stop at each of cols that
// fits.
func tabStopsAt(width int, cols []int) *uv.TabStops {
	ts := uv.DefaultTabStops(width)
	ts.Clear()
	for _, x := range cols {
		if x >= 0 && x < width {
			ts.Set(x)
		}
	}
	return ts
}

// Titles is the window title, the icon name and the title stack.
func (e *Emulator) Titles() Titles {
	t := Titles{Title: e.title, Icon: e.iconName}
	for _, s := range e.titleStack {
		t.Stack = append(t.Stack, TitleEntry{Title: s.title, Icon: s.icon, HasTitle: s.hasTitle, HasIcon: s.hasIcon})
	}
	return t
}

// RestoreTitles puts back the titles and the newest entries of the stack, as
// many as XTWINOPS 22 keeps.
func (e *Emulator) RestoreTitles(t Titles) {
	e.title, e.iconName = t.Title, t.Icon
	e.titleStack = nil
	for _, s := range t.Stack[max(len(t.Stack)-maxTitleStack, 0):] {
		e.titleStack = append(e.titleStack, savedTitle{title: s.Title, icon: s.Icon, hasTitle: s.HasTitle, hasIcon: s.HasIcon})
	}
}

// GuestColors is what the guest set with OSC 4, 10, 11 and 12.
func (e *Emulator) GuestColors() GuestColors {
	c := GuestColors{Palette: e.colors}
	if e.guestFg {
		c.Fg = e.fgColor
	}
	if e.guestBg {
		c.Bg = e.bgColor
	}
	if e.guestCur {
		c.Cursor = e.curColor
	}
	return c
}

// RestoreGuestColors replaces what the guest set with OSC 4, 10, 11 and 12. A
// nil default colour is one the guest has not set, which the theme's default
// answers for.
func (e *Emulator) RestoreGuestColors(c GuestColors) {
	e.colors = c.Palette
	e.refreshPaletteClaims()
	e.fgColor, e.guestFg = c.Fg, c.Fg != nil
	e.bgColor, e.guestBg = c.Bg, c.Bg != nil
	e.curColor, e.guestCur = c.Cursor, c.Cursor != nil
}

// ansiModes are the ANSI modes the emulator implements, which ANSIModes
// reports.
var ansiModes = []ansi.ANSIMode{ansi.ModeInsertReplace, ansi.ModeLineFeedNewLine}

// ANSIModes reports insert mode (4) and newline mode (20).
func (e *Emulator) ANSIModes() map[int]bool {
	modes := make(map[int]bool, len(ansiModes))
	for _, m := range ansiModes {
		modes[int(m)] = e.isModeSet(m)
	}
	return modes
}

// RestoreANSIModes sets the ANSI modes given. It goes through setMode, which
// also sets the flags the print and line feed paths read.
func (e *Emulator) RestoreANSIModes(modes map[int]bool) {
	for _, m := range ansiModes {
		on, ok := modes[int(m)]
		if !ok {
			continue
		}
		setting := ansi.ModeReset
		if on {
			setting = ansi.ModeSet
		}
		e.setMode(m, setting)
	}
}

// PendingInput is the input since the parser last left its ground state, with
// the controls it carried out inside the sequence left out. Inside a CSI or an
// escape sequence a C0 control is carried out where it is and the sequence
// goes on, so a replay that sent it again would move the cursor twice. Inside
// a string (OSC, DCS, SOS, PM, APC) a control is ignored or is payload, and is
// kept.
func (e *Emulator) PendingInput() []byte {
	if len(e.pending) == 0 {
		return nil
	}
	p := e.pending
	if len(p) < 2 || p[0] != ansi.ESC || !(p[1] == '[' || p[1] >= 0x20 && p[1] <= 0x2f || p[1] < 0x20) {
		return append([]byte(nil), p...)
	}
	out := make([]byte, 1, len(p))
	out[0] = p[0]
	for _, b := range p[1:] {
		if b >= 0x20 {
			out = append(out, b)
		}
	}
	return out
}

// RestorePendingInput drops the sequence the parser is in, and any character
// cluster left open across the last Write, and then reads p.
func (e *Emulator) RestorePendingInput(p []byte) {
	e.parser.state = parser.GroundState
	e.parser.clear()
	e.parser.dataLen = 0
	e.lastState = parser.GroundState
	e.grapheme = e.grapheme[:0]
	e.openGrapheme = openGrapheme{}
	e.dropPending()
	if len(p) > MaxPendingInput {
		p = p[:MaxPendingInput]
	}
	if len(p) > 0 {
		_, _ = e.Write(p)
	}
}

// pendingKeepCap is the largest buffer dropPending keeps for the next
// sequence. A kitty image grows it to megabytes, which a pane should not hold
// for the rest of its life.
const pendingKeepCap = 64 << 10

// dropPending empties the pending input at the end of a sequence.
func (e *Emulator) dropPending() {
	if cap(e.pending) > pendingKeepCap {
		e.pending = nil
		return
	}
	e.pending = e.pending[:0]
}

// notePending brings the pending input up to date at the end of a Write of
// p. start is where in p the parser last left its ground state, or -1 when
// it did not leave it in p: then p, all of it, continues the sequence the
// pending input already holds. It runs once a Write, not once a byte, so a
// flood of SGR pays a compare per byte and nothing more.
func (e *Emulator) notePending(p []byte, start int) {
	switch {
	case e.parser.State() == parser.GroundState:
		if len(e.pending) > 0 {
			e.dropPending()
		}
	case start >= 0:
		e.dropPending()
		e.appendPending(p[start:]...)
	default:
		e.appendPending(p...)
	}
}

// appendPending adds bytes to the pending input, up to MaxPendingInput. The
// parser keeps no more of one payload than that either, so the bytes past it
// change nothing a replay would show.
func (e *Emulator) appendPending(p ...byte) {
	if room := MaxPendingInput - len(e.pending); room < len(p) {
		p = p[:max(room, 0)]
	}
	e.pending = append(e.pending, p...)
}

// parseGuestColor reads a colour an OSC 4, 10, 11 or 12 sets, as a
// color.RGBA whatever form the guest wrote it in. XParseColor returns a
// different type for "#rrggbb" than for "rgb:r/g/b", and a cell keeps the type
// it was painted with, so the same colour written two ways made two cells
// that compare unequal, and a colour that went through a snapshot, which
// carries the value, came back as the other type.
func parseGuestColor(s string) color.Color {
	c := ansi.XParseColor(s)
	if c == nil {
		return nil
	}
	r, g, b, a := c.RGBA()
	return color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: uint8(a >> 8)} //nolint:gosec // RGBA is 16-bit
}
