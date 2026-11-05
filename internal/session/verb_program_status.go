package session

import (
	"encoding/json"
	"strings"

	"tuios.dev/progstatus"
)

// programStatusIntro is what comes before an OSC 7501 report's body.
const programStatusIntro = "\x1b]7501;"

// verbReportProgramStatus applies one OSC 7501 report to a pane as if the
// pane's emulator had parsed it. It is how tuios status reports from a process
// with no controlling terminal, such as an agent's shell tool, which captures
// standard output: the sequence written there never reaches the pane.
//
// The report goes through the same parser, the same store and the same
// lifetime rules as one the emulator read. Its process group is the pane's
// foreground group now, as the PTY reader would have read it, so a working
// record from a program in the foreground ends when that program does.
func (d *Daemon) verbReportProgramStatus(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		Report  string `json:"report"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Report == "" {
		return nil, invalidParam("report", "report is required: the body of one OSC 7501 report, such as state=working:app=build")
	}
	if progstatus.IsQuery([]byte(p.Report)) {
		return nil, invalidParam("report", "report is the feature query. Send a report with a state")
	}
	// The length the sequence would have on the terminal, OSC through ST, so
	// the parser holds it to the same limit.
	seqLen := len(programStatusIntro) + len(p.Report) + 2
	r, ok := progstatus.Parse([]byte(p.Report), seqLen)
	if !ok {
		return nil, invalidParam("report", "the report is not valid OSC 7501, so a terminal would discard it. Nothing was recorded")
	}

	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	target := p.Window
	if target == "" {
		id, err := focusedWindowID(sess.GetState())
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		target = id
	}
	state := sess.GetState()
	idx, err := findWindowStateIndex(state.Windows, target)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	win := state.Windows[idx]
	pty := sess.GetPTY(win.PTYID)
	if pty == nil {
		return nil, newVerbError(ErrVerbWindowNotFound, "window "+echoName(target)+" has no running pane")
	}
	// Under the terminal lock, as the emulator's callback runs: the report is
	// ordered with the pane's own output, and no chunk's process group is in
	// flight while it is read.
	pty.terminalMu.Lock()
	pty.noteProgramStatus(progstatus.Event{Report: r})
	pty.terminalMu.Unlock()
	pty.flushProgramStatus()

	return map[string]any{
		"type":           "program_status_reported",
		"window_id":      win.ID,
		"program_status": programStatusList(programStatusWire(pty.ProgramStatusRecords())),
	}, nil
}

// ProgramStatusBody is the body of an OSC 7501 sequence: the text between
// "ESC ] 7501;" and the string terminator, as report-program-status takes it.
func ProgramStatusBody(seq string) string {
	return strings.TrimSuffix(strings.TrimPrefix(seq, programStatusIntro), "\x1b\\")
}
