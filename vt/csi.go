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
	default:
		e.flushGrapheme()
	}

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

func (e *Emulator) reportSetting(req string) {
	r := e.scr.ScrollRegion()
	var value string
	switch req {
	case "r":
		value = fmt.Sprintf("%d;%dr", r.Min.Y+1, r.Max.Y)
	case "s":
		value = fmt.Sprintf("%d;%ds", r.Min.X+1, r.Max.X)
	case "m":
		value = sgrReport(e.scr.cur.Pen) + "m"
	case " q":
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
