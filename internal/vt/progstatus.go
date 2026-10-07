package vt

import (
	"io"

	"github.com/Gaurav-Gosain/tuios/internal/progstatus"
)

// The Program Status Protocol (OSC 7501) as both backends read it. The parsing
// and every discard rule are in internal/progstatus; this is the part that
// knows about the sequence's terminator and the emulator's reply pipe.

// parseProgramStatusOSC reads one OSC 7501 payload ("7501;..."). query says it
// is the feature detection query, which the caller answers with
// programStatusReply. Otherwise ok says the report passed every check and may
// be applied.
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

// programStatusReply is the feature detection reply, ended the way the query
// was. It is the only thing the protocol ever writes back: no id, title or
// message of any record is ever sent to the program.
func programStatusReply(bel bool) string {
	return progstatus.QueryReply + oscReplyEnd(bel)
}

// handleProgramStatus answers the query or hands a valid report to the
// ProgramStatus callback.
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
