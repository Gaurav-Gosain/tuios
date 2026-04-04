package vt

import "github.com/charmbracelet/x/ansi"

// handleDcs handles a DCS escape sequence.
func (e *Emulator) handleDcs(cmd ansi.Cmd, params ansi.Params, data []byte) {
	e.flushGrapheme() // Flush any pending grapheme before handling DCS sequences.
	if !e.handlers.handleDcs(cmd, params, data) {
		if e.passthroughDCS && e.cb.Passthrough != nil {
			// Build raw DCS sequence: ESC P <params> <data> ST
			raw := buildDCSSequence(cmd, params, data)
			e.cb.Passthrough(raw)
		} else {
			e.logf("unhandled sequence: DCS %q %q", paramsString(cmd, params), data)
		}
	}
}

// buildDCSSequence reconstructs a raw DCS escape sequence from parsed components.
func buildDCSSequence(cmd ansi.Cmd, params ansi.Params, data []byte) []byte {
	var raw []byte
	raw = append(raw, '\x1b', 'P') // DCS introducer
	raw = append(raw, []byte(paramsString(cmd, params))...)
	raw = append(raw, data...)
	raw = append(raw, '\x1b', '\\') // ST terminator
	return raw
}

// handleApc handles an APC escape sequence.
func (e *Emulator) handleApc(data []byte) {
	e.flushGrapheme() // Flush any pending grapheme before handling APC sequences.
	if !e.handlers.handleApc(data) {
		if e.passthroughAPC && e.cb.Passthrough != nil {
			// Build raw APC sequence: ESC _ <data> ST
			raw := make([]byte, 0, len(data)+4)
			raw = append(raw, '\x1b', '_') // APC introducer
			raw = append(raw, data...)
			raw = append(raw, '\x1b', '\\') // ST terminator
			e.cb.Passthrough(raw)
		} else {
			e.logf("unhandled sequence: APC %q", data)
		}
	}
}

// handleSos handles an SOS escape sequence.
func (e *Emulator) handleSos(data []byte) {
	e.flushGrapheme() // Flush any pending grapheme before handling SOS sequences.
	if !e.handlers.handleSos(data) {
		e.logf("unhandled sequence: SOS %q", data)
	}
}

// handlePm handles a PM escape sequence.
func (e *Emulator) handlePm(data []byte) {
	e.flushGrapheme() // Flush any pending grapheme before handling PM sequences.
	if !e.handlers.handlePm(data) {
		e.logf("unhandled sequence: PM %q", data)
	}
}
