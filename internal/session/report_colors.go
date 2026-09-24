package session

import (
	"image/color"

	"github.com/charmbracelet/x/ansi"
)

// The colours a program is told the terminal is drawn in.
//
// A program that wants to know whether it is on a dark or a light background
// asks with OSC 11, and one that wants the default text colour asks with
// OSC 10. In a daemon session it is the daemon's emulator that answers, since
// that is the one the program's bytes are parsed by. But the daemon draws
// nothing: what the pane sits on is the client's decision, and when the client
// paints a pane background (appearance.pane_background) the answer the
// emulator would give on its own is not what the user sees.
//
// So the client says, in the state it already pushes, and the daemon hands the
// pair to every emulator in the session. Empty is the client saying it paints
// nothing, which puts back the emulator's own answer.

// applyReportColors hands a pushed pair to every pane in the session, and to
// every pane made after it. A pair that did not change costs a comparison.
func (s *Session) applyReportColors(bgHex, fgHex string) {
	s.ptysMu.Lock()
	defer s.ptysMu.Unlock()
	if s.reportBgHex == bgHex && s.reportFgHex == fgHex {
		return
	}
	s.reportBgHex, s.reportFgHex = bgHex, fgHex
	s.reportBg, s.reportFg = parseReportColor(bgHex), parseReportColor(fgHex)
	for _, p := range s.ptys {
		p.setReportColors(s.reportFg, s.reportBg)
	}
}

// setReportColors gives this pane's emulator the pair, under the lock that
// guards it.
func (p *PTY) setReportColors(fg, bg color.Color) {
	p.terminalMu.Lock()
	defer p.terminalMu.Unlock()
	if p.terminal != nil {
		p.terminal.SetReportColors(fg, bg)
	}
}

// parseReportColor reads a #rrggbb colour, and nil for anything else, which is
// the emulator's own answer.
func parseReportColor(hex string) color.Color {
	if len(hex) != 7 || hex[0] != '#' {
		return nil
	}
	return ansi.XParseColor(hex)
}
