package vt

import (
	"bytes"
	"image/color"
	"slices"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/parser"

	uv "github.com/charmbracelet/ultraviolet"
)

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
	t := Titles{Title: e.title, Icon: e.iconName, ExplicitIcon: e.explicitIcon}
	for _, s := range e.titleStack {
		t.Stack = append(t.Stack, s.entry())
	}
	return t
}

// RestoreTitles puts back the titles and the newest entries of the stack, as
// many as XTWINOPS 22 keeps.
func (e *Emulator) RestoreTitles(t Titles) {
	e.title, e.iconName, e.explicitIcon = t.Title, t.Icon, t.ExplicitIcon
	e.titleStack = nil
	for _, s := range t.Stack[max(len(t.Stack)-maxTitleStack, 0):] {
		e.titleStack = append(e.titleStack, savedTitleOf(s))
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
	return withoutExecutedControls(e.pending)
}

func withoutExecutedControls(p []byte) []byte {
	out := make([]byte, 0, len(p))
	st, need := parser.GroundState, 0
	for i := 0; i < len(p); i++ {
		b := p[i]
		if st == parser.Utf8State {
			if b&0xc0 == 0x80 {
				if need--; need <= 0 {
					st = parser.GroundState
				}
				out = append(out, b)
				continue
			}
			st = parser.GroundState
		}
		if isStringState(st) {
			if n := stringPayloadRun(p[i:]); n > 0 {
				out = append(out, p[i:i+n]...)
				i += n - 1
				continue
			}
		}
		var action parser.Action
		st, action = parser.Table.Transition(st, b)
		if st == parser.Utf8State {
			need = utf8ByteLen(b) - 1
		}
		if action == parser.ExecuteAction && st != parser.GroundState {
			continue
		}
		out = append(out, b)
	}
	return out
}

// OpenCluster is a character cluster drawn at the end of the last Write that
// the next Write may still extend: a base whose skin tone, joiner or
// combining mark has not arrived yet. Text is the cluster as drawn, X and Y
// the cell it was drawn in, Width the columns it took, and Left and Right the
// margins it was drawn under.
type OpenCluster struct {
	Text        string
	X, Y        int
	Width       int
	Left, Right int
}

// OpenCluster returns the cluster left open across the last Write, and false
// when there is none. A snapshot taken between the parts of one cluster
// carries it, or the receiver draws the rest as a cluster of its own.
func (e *Emulator) OpenCluster() (OpenCluster, bool) {
	o := &e.openGrapheme
	if !o.active {
		return OpenCluster{}, false
	}
	return OpenCluster{Text: o.baseCluster(), X: o.x, Y: o.y, Width: o.width, Left: o.left, Right: o.right}, true
}

// RestoreOpenCluster opens c as if this emulator had just drawn it, so the
// next Write can extend it. Call it after RestorePendingInput, which closes
// any open cluster. A cluster outside the screen is ignored.
func (e *Emulator) RestoreOpenCluster(c OpenCluster) {
	if c.Text == "" || c.X < 0 || c.Y < 0 || c.X >= e.Width() || c.Y >= e.Height() || c.Width < 1 {
		return
	}
	var ascii byte
	base := c.Text
	if len(c.Text) == 1 && c.Text[0] < utf8.RuneSelf {
		ascii, base = c.Text[0], ""
	}
	e.openGrapheme.arm(c.X, c.Y, c.Width, c.Left, c.Right, ascii, base)
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

const pendingKeepCap = 64 << 10

func (e *Emulator) dropPending() {
	if cap(e.pending) > pendingKeepCap {
		e.pending = nil
		return
	}
	e.pending = e.pending[:0]
}

func (e *Emulator) notePending(p []byte, from parser.State, need int) {
	state := e.parser.State()
	if state == parser.GroundState {
		if len(e.pending) > 0 {
			e.dropPending()
		}
		return
	}
	start := -1
	if state == parser.Utf8State {
		for i := len(p) - 1; i >= 0 && i >= len(p)-utf8.UTFMax; i-- {
			if p[i]&0xc0 == 0xc0 {
				start = i
				break
			}
		}
	} else {
		start = bytes.LastIndexByte(p, ansi.ESC)
		if state == parser.DcsStringState || holdsC1Introducer(p[start+1:]) {
			start = sequenceStart(p, from, need)
		}
	}
	if start >= 0 {
		e.dropPending()
		e.appendPending(p[start:]...)
		return
	}
	e.appendPending(p...)
}

func holdsC1Introducer(p []byte) bool {
	return slices.ContainsFunc(p, isC1Introducer)
}

func isC1Introducer(b byte) bool {
	switch b {
	case 0x90, 0x98, 0x9b, 0x9d, 0x9e, 0x9f:
		return true
	}
	return false
}

func sequenceStart(p []byte, st parser.State, need int) int {
	start := -1
	for i := 0; i < len(p); i++ {
		b := p[i]
		if st == parser.Utf8State {
			if b&0xc0 == 0x80 {
				if need--; need <= 0 {
					st = parser.GroundState
				}
				continue
			}
			st = parser.GroundState
		}
		if isStringState(st) {
			if n := stringPayloadRun(p[i:]); n > 0 {
				i += n - 1
				continue
			}
		}
		prev := st
		st, _ = parser.Table.Transition(st, b)
		switch {
		case st == parser.Utf8State:
			need = utf8ByteLen(b) - 1
			start = i
		case b == ansi.ESC:
			if st == parser.EscapeState {
				start = i
			}
		case isC1Introducer(b), prev == parser.GroundState && st != parser.GroundState:
			start = i
		}
	}
	return start
}

func (e *Emulator) appendPending(p ...byte) {
	if room := MaxPendingInput - len(e.pending); room < len(p) {
		p = p[:max(room, 0)]
	}
	e.pending = append(e.pending, p...)
}

func parseGuestColor(s string) color.Color {
	c := ansi.XParseColor(s)
	if c == nil {
		return nil
	}
	r, g, b, a := c.RGBA()
	return color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: uint8(a >> 8)} //nolint:gosec // RGBA is 16-bit
}
