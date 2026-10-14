package vt

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

func (e *Emulator) handleCsi(cmd ansi.Cmd, params ansi.Params) {
	switch cmd.Final() {
	case 'm', 'b':
		// SGR and REP neither print nor move the cursor, so they do not end
		// the cluster in flight: ghostty keeps pairing regional indicators
		// across both. Closing on REP also left the repeats unpaired, as
		// cells no frame can re-express.
	default:
		e.flushGrapheme() // Flush any pending grapheme before handling CSI sequences.
	}

	// Debug logging for CSI 't' sequences (XTWINOPS)
	if cmd.Final() == 't' {
		if l := debugLogger(); l != nil {
			l.Printf("VT-CSI: received CSI %q (cmd=%d, final=%c)", paramsString(cmd, params), int(cmd), cmd.Final())
		}
	}

	if !e.handlers.handleCsi(cmd, params) {
		e.logf("unhandled sequence: CSI %q", paramsString(cmd, params))
	}
}

func (e *Emulator) handleRequestMode(params ansi.Params, isAnsi bool) {
	n, _, ok := params.Param(0, 0)
	if !ok || n == 0 {
		return
	}

	var mode ansi.Mode = ansi.DECMode(n)
	if isAnsi {
		mode = ansi.ANSIMode(n)
	}

	_, _ = io.WriteString(e.pipe, ansi.ReportMode(mode, e.modeSetting(mode)))
}

// reportSetting answers a DECRQSS request for the current value of a setting.
//
// The reply is DCS Ps $ r <value> ST, with Ps 1 for a setting this emulator
// reports and 0 for one it does not. Refusing is not the same as staying
// silent, which is why the default branch answers at all: a guest that asks and
// hears nothing waits out a timeout before deciding, and a zero tells it
// immediately.
//
// The settings reported are the ones this emulator holds exactly: the margins,
// the pen (SGR) and the cursor shape (DECSCUSR). Each value is written so that
// sending it back recreates the state it was read from. DECSCA is refused, as
// libghostty refuses it, so a guest hears the same answer on either backend.
func (e *Emulator) reportSetting(req string) {
	r := e.scr.ScrollRegion()
	var value string
	switch req {
	case "r": // DECSTBM
		value = fmt.Sprintf("%d;%dr", r.Min.Y+1, r.Max.Y)
	case "s": // DECSLRM
		value = fmt.Sprintf("%d;%ds", r.Min.X+1, r.Max.X)
	case "m": // SGR
		value = sgrReport(e.scr.cur.Pen) + "m"
	case " q": // DECSCUSR
		value = fmt.Sprintf("%d q", cursorStyleReport(e.cursorStyle, e.cursorSteady))
	default:
		_, _ = io.WriteString(e.pipe, "\x1bP0$r\x1b\\")
		return
	}
	_, _ = io.WriteString(e.pipe, "\x1bP1$r"+value+"\x1b\\")
}

func paramsString(cmd ansi.Cmd, params ansi.Params) string {
	var s strings.Builder
	if mark := cmd.Prefix(); mark != 0 {
		s.WriteByte(mark)
	}
	params.ForEach(-1, func(i, p int, more bool) {
		fmt.Fprintf(&s, "%d", p)
		if i < len(params)-1 {
			if more {
				s.WriteByte(':')
			} else {
				s.WriteByte(';')
			}
		}
	})
	if inter := cmd.Intermediate(); inter != 0 {
		s.WriteByte(inter)
	}
	if final := cmd.Final(); final != 0 {
		s.WriteByte(final)
	}
	return s.String()
}
