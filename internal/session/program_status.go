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
	"github.com/Gaurav-Gosain/tuios/internal/progstatus"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// The Program Status Protocol (OSC 7501) in the daemon.
//
// Any program in any pane can say what it is doing: cargo, terraform, brew, a
// deploy script or a coding agent. The pane's emulator parses each report
// (internal/vt, internal/progstatus) and the PTY keeps the records, with the
// specification's limits, its id hierarchy and its lifetime: a shell prompt
// and the exit of the pane's process end working, blocked and idle records,
// a full reset ends all of them, and done and error stay until the person
// types into the pane.
//
// The records reach clients as WindowState.ProgramStatus, and the most urgent
// one becomes the pane's agent state through AgentSourceProgram, a source as
// trusted as a harness hook: the program is saying what it is doing, which is
// better than anything the screen or the process table can work out.

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

// AgentSourceProgram ranks just below AgentSourceReport. See agent_source.go.

// noteProgramStatus is the emulator's ProgramStatus callback. It runs with the
// terminal lock held, so it only records; flushProgramStatus hands the change
// on after the write.
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

// reportGroup is the process group a working, blocked or idle report came
// from, when that group held the pane's foreground and is not the pane's own
// process, and 0 otherwise. A record with a group ends when the group does
// (endProgramStatusOfEndedGroups); a record without one, from a background
// job or from a pane whose own process is the program, stays until the
// program changes it or a prompt starts.
//
// The group is read by the PTY reader, as the chunk holding the report comes
// off the terminal (see readerReportGroup), because by the time the emulator
// parses the chunk a fast program may have exited. Only a report the reader
// did not see whole, split across two reads, is read here, late.
//
// A record already armed keeps its group when the read finds none. Even the
// reader's read can come too late: a program that reports and exits at once
// may have handed the foreground back to the shell before the chunk is off the
// terminal. That read finds the shell, and taking it as the answer disarmed the
// record, which then outlived its program for good. A report that replaces an
// armed record is taken as coming from the same program.
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

// foregroundReportGroup reads which process group holds the pane's terminal,
// and returns it when it is not the pane's own process: on this machine from
// the kernel, for a pane on another machine from what that machine last said
// (at most two seconds old). It returns 0 when the pane's own process holds
// the foreground or nothing can be read.
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

// programStatusMarker is what a chunk holding an OSC 7501 report contains.
var programStatusMarker = []byte("]7501;")

// readerReportGroup is the group for vtChunk.group: 0 when data holds no OSC
// 7501, else the foreground group plus one (so 1 is "read, none").
func (p *PTY) readerReportGroup(data []byte) int64 {
	if !bytes.Contains(data, programStatusMarker) {
		return 0
	}
	return int64(p.foregroundReportGroup()) + 1
}

// noteProgramStatusMark ends the working, blocked and idle records when a new
// shell prompt begins (OSC 133 A).
func (p *PTY) noteProgramStatusMark(m vt.SemanticMarker) {
	if m.Type == vt.MarkerPromptStart && p.progStatus.DropTransient() {
		p.progStatusDirty.Store(true)
	}
}

// noteProgramStatusInput ends the done and error records when the person
// types into the pane. A focus report is the terminal speaking, not the
// person, so it does not count.
func (p *PTY) noteProgramStatusInput(data []byte) {
	if len(data) == 0 || isFocusReport(data) || !p.progStatus.HasFinished() {
		return
	}
	if p.progStatus.DropFinished() {
		p.progStatusDirty.Store(true)
		p.flushProgramStatus()
	}
}

// isFocusReport reports whether data is only focus in or focus out reports.
func isFocusReport(data []byte) bool {
	for len(data) > 0 {
		if len(data) < 3 || data[0] != 0x1b || data[1] != '[' || (data[2] != 'I' && data[2] != 'O') {
			return false
		}
		data = data[3:]
	}
	return true
}

// flushProgramStatus tells the session the records changed, once per change.
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

// programStatusText makes a decoded title or msg safe to draw outside the
// grid. The parser has already refused control characters; this removes the
// characters that draw nothing but change how text reads, bidi overrides
// among them, so a record cannot reorder or hide words.
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

// programStatusWire turns the store's records into the wire form.
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

// programStatusAgentState maps a record's state onto tuios's agent state.
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

// programStatusBlockedBy maps a blocked record's kind onto blocked_by.
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

// programStatusList is the records as a verb reports them: an empty list, not
// null, when there are none.
func programStatusList(recs []ProgramStatusRecord) []ProgramStatusRecord {
	if recs == nil {
		return []ProgramStatusRecord{}
	}
	return recs
}

// errProgramStatusSame tells mutateState the records did not change.
var errProgramStatusSame = errors.New("program status unchanged")

// applyProgramStatus copies a pane's records into its window state and, when
// the agent features are on, sets the pane's agent state from the summary
// record. It runs on the goroutine that raised eventProgramStatus, never under
// the terminal lock.
func (s *Session) applyProgramStatus(windowID, ptyID string, agents bool) (released bool) {
	pty := s.GetPTY(ptyID)
	if pty == nil {
		return false
	}
	// The vtWriter, the detector, the input handler and the exit path can
	// each raise this at once. Held from the read of the records to the
	// last write, so a slower caller cannot write an older view over a
	// newer one.
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
		// Empty when the program did not say. It is not guessed from the
		// message: the specification forbids reading meaning into msg (see
		// agentKindOf).
		r.Kind = programStatusBlockedBy(sum.Kind)
	}
	s.yieldNoneClaim(windowID)
	_, _, _ = s.ApplyAgentReport(windowID, r)
	return false
}

// yieldNoneClaim lets go of a window whose claim is held by a source stronger
// than the program and says none: a harness that reported it left the pane.
// Such a claim holds nothing worth keeping, so a program that reports in the
// pane afterwards is heard. Any other stronger claim keeps the pane, and the
// program's report is refused.
func (s *Session) yieldNoneClaim(windowID string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	claim, held := s.agentClaims[windowID]
	if !held || claim.source.rank() <= AgentSourceProgram.rank() || claim.auto {
		return
	}
	if idx, err := findWindowStateIndex(s.state.Windows, windowID); err == nil &&
		s.state.Windows[idx].AgentState == AgentStateNone {
		delete(s.agentClaims, windowID)
	}
}

// endProgramStatusOfEndedGroups is the agent detector's look at a pane: the
// working, blocked and idle records whose process group has ended go, since
// the program that reported them has exited. info and running are the
// detector's reading of the pane's foreground.
//
// It is the process-exit rule of the specification for a pane whose shell
// does not mark its prompt with OSC 133: without it a program that crashed
// before it reported done would leave a working record behind for good, since
// the protocol has no heartbeat. Each record carries its own group, so a
// foreground program exiting ends its own records and leaves a background
// job's alone.
//
// On this machine a group has ended when no process is left in it. For a pane
// on another machine, whose processes cannot be asked, it has ended when that
// machine says the pane's shell holds the foreground again. The caller holds
// no lock.
func (p *PTY) endProgramStatusOfEndedGroups(info foregroundInfo, running bool) {
	if len(p.progStatus.Groups()) == 0 {
		return
	}
	_, _, remote := p.remoteForeground()
	ended := func(group int) bool {
		if remote {
			return running && info.pid > 0 && info.pid == info.shellPID && info.pid != group
		}
		return !processGroupAlive(group)
	}
	if p.progStatus.DropEnded(ended) {
		p.progStatusDirty.Store(true)
		p.flushProgramStatus()
	}
}

// releaseProgramStatusClaim clears the pane's agent state when the program's
// records were what set it and none is left, lets go of the pane, and reports
// whether it did. The caller then has the detector and the screen tier look at
// the pane again, so a state that is still true comes back from what is true
// now: a state a weaker source held before the program took the pane may be
// stale, so it is not replayed. A claim another source holds is left alone:
// the records ending says nothing about a harness that reports for itself.
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
