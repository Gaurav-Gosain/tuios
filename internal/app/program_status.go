package app

import (
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"tuios.dev/progstatus"
)

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

func programProgressText(r sessiontree.ProgramRecord) string {
	if r.Progress < 0 || r.Progress > 100 {
		return ""
	}
	if r.State != string(progstatus.Working) && r.State != string(progstatus.Blocked) {
		return ""
	}
	return strconv.Itoa(r.Progress) + "%"
}

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

func (m *OS) paneProgramStatus(sessionID, windowID string) []sessiontree.ProgramRecord {
	w, sum := m.findPane(sessionID, windowID)
	switch {
	case w != nil:
		return w.ProgramStatus
	case sum != nil:
		return programStatusFromWire(nil, sum.ProgramStatus)
	}
	return nil
}
