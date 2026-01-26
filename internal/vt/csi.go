package vt

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func (e *Emulator) handleCsi(cmd ansi.Cmd, params ansi.Params) {
	e.flushGrapheme() // Flush any pending grapheme before handling CSI sequences.

	// Debug logging for CSI 't' sequences (XTWINOPS)
	if cmd.Final() == 't' && os.Getenv("TUIOS_DEBUG_INTERNAL") == "1" {
		if f, err := os.OpenFile("/tmp/tuios-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
			_, _ = fmt.Fprintf(f, "[%s] VT-CSI: received CSI %q (cmd=%d, final=%c)\n",
				time.Now().Format("15:04:05.000"), paramsString(cmd, params), int(cmd), cmd.Final())
			_ = f.Close()
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

	setting := e.modes[mode]
	_, _ = io.WriteString(e.pw, ansi.ReportMode(mode, setting))
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
