package session

import (
	"image/color"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// The colours a program is told the terminal is drawn in.
//
// A program that wants to know whether it is on a dark or a light background
// asks with OSC 11, and one that wants the default text colour asks with
// OSC 10. In a daemon session it is the daemon's emulator that answers, since
// that is the one the program's bytes are parsed by. But the daemon draws
// nothing and has no terminal to ask: what the pane sits on is the client's
// decision, and the colours of the terminal around it are only known to the
// client. When the client paints a pane background
// (appearance.pane_background), or runs with no theme on a terminal that
// said what its own colours are, the answer the emulator would give on its
// own (black and white) is not what the user sees.
//
// So the client says, in the state it already pushes, and the daemon hands the
// pair, and the host's sixteen for OSC 4, to every emulator in the session.
// Empty is the client saying it has nothing to report, which puts back the
// emulator's own answer.
//
// With several clients attached, the client the person last gave input at
// decides (see ownsReportColors). Two clients on different themes would
// otherwise take turns with every push, and each turn would send every pane
// with mode 2031 on a colour scheme notice.

// ownsReportColors reports whether a push from cs may set the session's
// report colours: no other TUI client attached to the session has had input
// from its person more recently, by MsgClientActivity, the signal the latest
// window_size policy reads. A tie, such as two clients that have had no input
// yet, lets the push through, as before.
func (d *Daemon) ownsReportColors(cs *connState, sessionID string) bool {
	cs.mu.Lock()
	mine := cs.lastActivity
	cs.mu.Unlock()
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()
	for _, other := range d.clients {
		if other == cs {
			continue
		}
		other.mu.Lock()
		newer := other.sessionID == sessionID && other.isTUIClient && other.lastActivity.After(mine)
		other.mu.Unlock()
		if newer {
			return false
		}
	}
	return true
}

// applyReportColors hands a pushed pair and palette to every pane in the
// session, and to every pane made after it. What did not change costs a
// comparison.
func (s *Session) applyReportColors(bgHex, fgHex, palHex string) {
	s.ptysMu.Lock()
	defer s.ptysMu.Unlock()
	if s.reportsHeld {
		s.reportHeld = &heldReportColors{Bg: bgHex, Fg: fgHex, Palette: palHex}
		return
	}
	if s.reportBgHex == bgHex && s.reportFgHex == fgHex && s.reportPalHex == palHex {
		return
	}
	s.reportBgHex, s.reportFgHex, s.reportPalHex = bgHex, fgHex, palHex
	s.reportBg, s.reportFg = parseReportColor(bgHex), parseReportColor(fgHex)
	s.reportPal = parseReportPalette(palHex)
	for _, p := range s.ptys {
		p.setReportColors(s.reportFg, s.reportBg, s.reportPal)
	}
}

// heldReportColors is a push of the colours held by holdReportColors.
type heldReportColors struct {
	Bg, Fg, Palette string
}

// holdReportColors keeps every push of the colours from the emulators until
// releaseReportColors, for a handoff from before its panes stop until the
// exec. A push in that span would tell a program with mode 2031 on that the
// scheme changed, from an emulator already written down with the colours
// before it, and the new image would answer with those. A push held there is
// applied when the handoff is put back, and after the exec it comes again
// from the clients, which every one attaches again and pushes its colours.
func (s *Session) holdReportColors() {
	s.ptysMu.Lock()
	defer s.ptysMu.Unlock()
	s.reportsHeld = true
}

// releaseReportColors ends holdReportColors and applies the last push held.
func (s *Session) releaseReportColors() {
	s.ptysMu.Lock()
	held := s.reportHeld
	s.reportsHeld, s.reportHeld = false, nil
	s.ptysMu.Unlock()
	if held != nil {
		s.applyReportColors(held.Bg, held.Fg, held.Palette)
	}
}

// setReportColors gives this pane's emulator the pair and the palette, under
// the lock that guards it.
func (p *PTY) setReportColors(fg, bg color.Color, pal [16]color.Color) {
	p.terminalMu.Lock()
	defer p.terminalMu.Unlock()
	if p.terminal != nil {
		p.terminal.SetReportColors(fg, bg, pal)
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

// parseReportPalette reads up to sixteen comma-separated #rrggbb entries. An
// entry that is empty or malformed leaves its slot nil, and anything past the
// sixteenth is ignored, so a push cannot make this do more than sixteen
// parses however long it is.
func parseReportPalette(list string) [16]color.Color {
	var pal [16]color.Color
	for i := 0; i < len(pal) && list != ""; i++ {
		entry, rest, _ := strings.Cut(list, ",")
		pal[i] = parseReportColor(entry)
		list = rest
	}
	return pal
}
