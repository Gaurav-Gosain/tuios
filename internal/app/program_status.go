package app

import (
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/progstatus"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// A pane's OSC 7501 records (the Program Status Protocol), as the client draws
// them. The daemon parses and stores them and sets the pane's agent state from
// the most urgent one; the client shows what the agent state cannot carry: the
// program's own name, its progress, and every record when it reported more
// than one. Everything here is the program's own text, so it is drawn through
// printableTitle like every other string a pane wrote.

// programStatusFromWire converts the synced records, keeping cur when nothing
// changed so a redraw does not churn the slice.
func programStatusFromWire(cur []sessiontree.ProgramRecord, in []session.ProgramStatusRecord) []sessiontree.ProgramRecord {
	if len(in) == 0 {
		return nil
	}
	out := make([]sessiontree.ProgramRecord, len(in))
	for i, r := range in {
		out[i] = sessiontree.ProgramRecord{
			ID: r.ID, State: r.State, Kind: r.Kind, Progress: r.Progress,
			App: r.App, Title: r.Title, Msg: r.Msg, At: r.At,
		}
	}
	if slices.Equal(cur, out) {
		return cur
	}
	return out
}

// programSummary is the record a pane shows, picked the way the daemon picks
// the one its agent state comes from (progstatus.Summary): the most urgent,
// the root among equals, then the newest.
func programSummary(recs []sessiontree.ProgramRecord) (sessiontree.ProgramRecord, bool) {
	conv := make([]progstatus.Record, len(recs))
	for i, r := range recs {
		conv[i] = progstatus.Record{ID: r.ID, State: progstatus.State(r.State), Seq: uint64(r.At)}
	}
	best, ok := progstatus.Summary(conv)
	if !ok {
		return sessiontree.ProgramRecord{}, false
	}
	for _, r := range recs {
		if r.ID == best.ID {
			return r, true
		}
	}
	return sessiontree.ProgramRecord{}, false
}

// programProgressText is "40%" for a working or blocked record that carries a
// progress, and "" otherwise.
func programProgressText(r sessiontree.ProgramRecord) string {
	if r.Progress < 0 || r.Progress > 100 {
		return ""
	}
	if r.State != string(progstatus.Working) && r.State != string(progstatus.Blocked) {
		return ""
	}
	return strconv.Itoa(r.Progress) + "%"
}

// programRecordLine is one record as a line of a detail panel:
// "build/test: cargo working 40%: Compiling". The id is left out for the root
// record, the app when the record has none.
func programRecordLine(r sessiontree.ProgramRecord) string {
	var head []string
	if app := printableTitle(r.App); app != "" {
		head = append(head, app)
	}
	state := r.State
	if r.State == string(progstatus.Blocked) && r.Kind != "" {
		state += " (" + r.Kind + ")"
	}
	head = append(head, state)
	if p := programProgressText(r); p != "" {
		head = append(head, p)
	}
	line := strings.Join(head, " ")
	if r.ID != "" {
		line = printableTitle(r.ID) + ": " + line
	}
	text := printableTitle(r.Msg)
	if title := printableTitle(r.Title); title != "" {
		if text == "" {
			text = title
		} else {
			text = title + ": " + text
		}
	}
	if text != "" {
		line += ": " + text
	}
	return line
}

// programStatusLines are the detail lines for a pane's records, under a
// heading. A pane with one record says it in one line, since the row above
// already shows the summary.
func programStatusLines(recs []sessiontree.ProgramRecord) []string {
	if len(recs) == 0 {
		return nil
	}
	lines := make([]string, 0, len(recs)+1)
	lines = append(lines, "Program status:")
	for _, r := range recs {
		lines = append(lines, "  "+programRecordLine(r))
	}
	return lines
}

// paneProgramStatus is the records a pane of this machine reported, as this
// client holds them.
func (m *OS) paneProgramStatus(sessionID, windowID string) []sessiontree.ProgramRecord {
	if sessionID == "" || sessionID == m.sidebarCurrentSessionID() {
		for _, w := range m.Windows {
			if w != nil && w.ID == windowID {
				return w.ProgramStatus
			}
		}
	}
	if m.DaemonClient == nil || m.AttachedHost != "" {
		return nil
	}
	for _, w := range m.DaemonClient.SessionWindows(sessionID) {
		if w.ID == windowID {
			return programStatusFromWire(nil, w.ProgramStatus)
		}
	}
	return nil
}
