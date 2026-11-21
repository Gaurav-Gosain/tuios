package vt

import (
	"io"

	"tuios.dev/progstatus"
)

func parseProgramStatusOSC(payload []byte, bel bool) (r progstatus.Report, query, ok bool) {
	const prefix = "7501;"
	if len(payload) < len(prefix) || string(payload[:len(prefix)]) != prefix {
		return r, false, false
	}
	body := payload[len(prefix):]
	if progstatus.IsQuery(body) {
		return r, true, false
	}
	r, ok = progstatus.Parse(body, progstatus.SequenceLen(len(payload), bel))
	return r, false, ok
}

func programStatusReply(bel bool) string {
	return progstatus.QueryReply + oscReplyEnd(bel)
}

func (e *Emulator) handleProgramStatus(data []byte) {
	r, query, ok := parseProgramStatusOSC(data, e.parser.oscBEL)
	if query {
		_, _ = io.WriteString(e.pipe, programStatusReply(e.parser.oscBEL))
		return
	}
	if ok && e.cb.ProgramStatus != nil {
		e.cb.ProgramStatus(progstatus.Event{Report: r})
	}
}
