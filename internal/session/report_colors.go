package session

import (
	"image/color"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

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

type heldReportColors struct {
	Bg, Fg, Palette string
}

func (s *Session) holdReportColors() {
	s.ptysMu.Lock()
	defer s.ptysMu.Unlock()
	s.reportsHeld = true
}

func (s *Session) releaseReportColors() {
	s.ptysMu.Lock()
	held := s.reportHeld
	s.reportsHeld, s.reportHeld = false, nil
	s.ptysMu.Unlock()
	if held != nil {
		s.applyReportColors(held.Bg, held.Fg, held.Palette)
	}
}

func (p *PTY) setReportColors(fg, bg color.Color, pal [16]color.Color) {
	p.terminalMu.Lock()
	defer p.terminalMu.Unlock()
	if p.terminal != nil {
		p.terminal.SetReportColors(fg, bg, pal)
	}
}

func parseReportColor(hex string) color.Color {
	if len(hex) != 7 || hex[0] != '#' {
		return nil
	}
	return ansi.XParseColor(hex)
}

func parseReportPalette(list string) [16]color.Color {
	var pal [16]color.Color
	for i := 0; i < len(pal) && list != ""; i++ {
		entry, rest, _ := strings.Cut(list, ",")
		pal[i] = parseReportColor(entry)
		list = rest
	}
	return pal
}
