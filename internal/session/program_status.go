package session

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/invisible"
	"tuios.dev/progstatus"
	"tuios.dev/vt"
)

// ProgramStatusRecord is one OSC 7501 record as clients see it.
type ProgramStatusRecord struct {
	// ID is the record's id, empty for the root record.
	ID string `json:"id,omitempty"`
	// State is idle, working, done, blocked or error.
	State string `json:"state"`
	// Kind is what a blocked record waits for: permission, question or auth.
	Kind string `json:"kind,omitempty"`
	// Progress is 0 to 100, or -1 when the record carries none. It is always
	// sent, because 0 is a real value.
	Progress int `json:"progress"`
	// App is the record's app, or the nearest ancestor's when it has none.
	App string `json:"app,omitempty"`
	// Title and Msg are the decoded texts, with invisible formatting removed.
	Title string `json:"title,omitempty"`
	Msg   string `json:"msg,omitempty"`
	// At is when the record was last replaced, in Unix nanoseconds.
	At int64 `json:"at,omitempty"`
}

func (p *PTY) noteProgramStatus(ev progstatus.Event) {
	group := 0
	switch ev.Report.State {
	case progstatus.Working, progstatus.Blocked, progstatus.Idle:
		group = p.reportGroup(ev.Report.ID)
	}
	if p.progStatus.HandleFrom(ev, time.Now().UnixNano(), group) {
		p.progStatusDirty.Store(true)
	}
}

func (p *PTY) reportGroup(id string) int {
	if c := p.chunkGroup.Load(); c > 0 {
		if g := int(c - 1); g != 0 {
			return g
		}
		return p.progStatus.GroupOf(id)
	}
	if g := p.progStatus.GroupOf(id); g != 0 {
		return g
	}
	return p.foregroundReportGroup()
}

func (p *PTY) foregroundReportGroup() int {
	if shell := p.ShellPID(); shell > 0 {
		if pgid, ok := readForegroundPGID(shell); ok && pgid > 0 && pgid != shell {
			return pgid
		}
		return 0
	}
	if info, running, remote := p.remoteForeground(); remote && running && info.pid > 0 && info.pid != info.shellPID {
		return info.pid
	}
	return 0
}

var programStatusMarker = []byte("]7501;")

func (p *PTY) readerReportGroup(data []byte) int64 {
	if !bytes.Contains(data, programStatusMarker) {
		return 0
	}
	return int64(p.foregroundReportGroup()) + 1
}

func (p *PTY) noteProgramStatusMark(m vt.SemanticMarker) {
	if m.Type == vt.MarkerPromptStart && p.progStatus.DropTransient() {
		p.progStatusDirty.Store(true)
	}
}

func (p *PTY) noteProgramStatusInput(data []byte) {
	if len(data) == 0 || isFocusReport(data) || !p.progStatus.HasFinished() {
		return
	}
	if p.progStatus.DropFinished() {
		p.progStatusDirty.Store(true)
		p.flushProgramStatus()
	}
}

func isFocusReport(data []byte) bool {
	for len(data) > 0 {
		if len(data) < 3 || data[0] != 0x1b || data[1] != '[' || (data[2] != 'I' && data[2] != 'O') {
			return false
		}
		data = data[3:]
	}
	return true
}

func (p *PTY) flushProgramStatus() {
	if p.progStatusDirty.Swap(false) && p.emit != nil {
		p.emit(SessionEvent{Type: eventProgramStatus})
	}
}

// ProgramStatusSeen reports whether the pane sent an OSC 7501 report since
// its last full reset. Once it has, OSC 9;4 no longer sets the pane's agent
// state: a mapped progress report would wipe out the kind and the message of
// the program's own report.
func (p *PTY) ProgramStatusSeen() bool {
	return p.progStatus.Seen()
}

// ProgramStatusRecords returns the pane's records.
func (p *PTY) ProgramStatusRecords() []progstatus.Record {
	return p.progStatus.Records()
}

func programStatusText(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == utf8.RuneError || progstatus.IsControl(r) || invisible.Rune(r) {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

func programStatusWire(recs []progstatus.Record) []ProgramStatusRecord {
	if len(recs) == 0 {
		return nil
	}
	out := make([]ProgramStatusRecord, len(recs))
	for i, r := range recs {
		out[i] = ProgramStatusRecord{
			ID:       r.ID,
			State:    string(r.State),
			Kind:     string(r.Kind),
			Progress: r.Progress,
			App:      r.EffectiveApp,
			Title:    programStatusText(r.Title),
			Msg:      programStatusText(r.Msg),
			At:       r.At,
		}
	}
	return out
}

// ProgramStatusSummary picks the record a pane shows from its wire records:
// the most urgent state wins, the root among equals, then the newest. See
// progstatus.Summary.
func ProgramStatusSummary(recs []ProgramStatusRecord) (ProgramStatusRecord, bool) {
	if len(recs) == 0 {
		return ProgramStatusRecord{}, false
	}
	conv := make([]progstatus.Record, len(recs))
	for i, r := range recs {
		conv[i] = progstatus.Record{ID: r.ID, State: progstatus.State(r.State), Seq: uint64(r.At)}
	}
	best, ok := progstatus.Summary(conv)
	if !ok {
		return ProgramStatusRecord{}, false
	}
	for _, r := range recs {
		if r.ID == best.ID {
			return r, true
		}
	}
	return ProgramStatusRecord{}, false
}

// ProgramStatusMessage is the one line a record says: its title and its msg,
// joined with ": " when it has both.
func ProgramStatusMessage(r ProgramStatusRecord) string {
	switch {
	case r.Title != "" && r.Msg != "":
		return r.Title + ": " + r.Msg
	case r.Msg != "":
		return r.Msg
	}
	return r.Title
}

func programStatusAgentState(state string) (AgentState, bool) {
	switch progstatus.State(state) {
	case progstatus.Working:
		return AgentStateWorking, true
	case progstatus.Blocked:
		return AgentStateNeedsInput, true
	case progstatus.Done:
		return AgentStateDone, true
	case progstatus.Error:
		return AgentStateErrored, true
	case progstatus.Idle:
		return AgentStateIdle, true
	}
	return AgentStateNone, false
}

func programStatusBlockedBy(kind string) string {
	switch progstatus.Kind(kind) {
	case progstatus.Permission:
		return harness.PromptKindApproval
	case progstatus.Question:
		return harness.PromptKindQuestion
	case progstatus.Auth:
		return harness.PromptKindAuth
	}
	return ""
}

func programStatusList(recs []ProgramStatusRecord) []ProgramStatusRecord {
	if recs == nil {
		return []ProgramStatusRecord{}
	}
	return recs
}

var errProgramStatusSame = errors.New("program status unchanged")

func (s *Session) applyProgramStatus(windowID, ptyID string, agents bool) (released bool) {
	pty := s.GetPTY(ptyID)
	if pty == nil {
		return false
	}
	pty.progStatusApplyMu.Lock()
	defer pty.progStatusApplyMu.Unlock()
	wire := programStatusWire(pty.ProgramStatusRecords())
	_ = s.mutateState(func(st *SessionState) error {
		idx, err := findWindowStateIndex(st.Windows, windowID)
		if err != nil {
			return err
		}
		w := &st.Windows[idx]
		if slices.Equal(w.ProgramStatus, wire) {
			return errProgramStatusSame
		}
		w.ProgramStatus = wire
		return nil
	})
	if !agents {
		return false
	}
	sum, ok := ProgramStatusSummary(wire)
	if !ok {
		return s.releaseProgramStatusClaim(windowID)
	}
	state, _ := programStatusAgentState(sum.State)
	r := AgentReport{
		State:   state,
		Message: ProgramStatusMessage(sum),
		Source:  AgentSourceProgram,
	}
	if state == AgentStateNeedsInput {
		r.Kind = programStatusBlockedBy(sum.Kind)
	}
	s.yieldNoneClaim(windowID)
	_, _, _ = s.ApplyAgentReport(windowID, r)
	return false
}

func (s *Session) yieldNoneClaim(windowID string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	claim, held := s.agentClaims[windowID]
	if !held || claim.source.Rank() <= AgentSourceProgram.Rank() {
		return
	}
	if idx, err := findWindowStateIndex(s.state.Windows, windowID); err == nil &&
		s.state.Windows[idx].AgentState == AgentStateNone {
		if claim.auto || claim.sawProcess {
			s.setAgentClaim(windowID, agentClaim{
				source: AgentSourceDetect, auto: claim.auto, sawProcess: claim.sawProcess,
			})
		} else {
			delete(s.agentClaims, windowID)
		}
	}
}

func (p *PTY) endProgramStatusOfEndedGroups(info foregroundInfo, running bool) {
	if len(p.progStatus.Groups()) == 0 {
		return
	}
	_, _, remote := p.remoteForeground()
	ended := func(group int) bool {
		if remote {
			return running && info.pid > 0 && info.pid == info.shellPID && info.pid != group
		}
		return !ProcessGroupAlive(group)
	}
	if p.progStatus.DropEnded(ended) {
		p.progStatusDirty.Store(true)
		p.flushProgramStatus()
	}
}

func (s *Session) releaseProgramStatusClaim(windowID string) bool {
	s.stateMu.RLock()
	claim, held := s.agentClaims[windowID]
	s.stateMu.RUnlock()
	if !held || claim.source != AgentSourceProgram {
		return false
	}
	_, _, _ = s.ApplyAgentReport(windowID, AgentReport{State: AgentStateNone, Source: AgentSourceProgram})
	s.yieldAgentClaim(windowID, AgentSourceProgram)
	return true
}
