package session

import (
	"errors"
	"slices"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/agentstate"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

func clearAgentNote(w *WindowState) {
	w.AgentMessage = ""
	w.AgentKind = ""
}

// AgentState is the semantic state of an agent (a coding-agent CLI or any other
// long-running process) running in a window's pane. It is daemon-owned per-window
// state: a pane reports its own state through the set-agent-state verb, and the
// daemon syncs it to attached clients alongside the rest of the window state.
// The type, its names and its ranks are in internal/agentstate.
type AgentState = agentstate.State

// The states, under the names this package has always used.
const (
	AgentStateNone         = agentstate.None
	AgentStateWorking      = agentstate.Working
	AgentStateNeedsInput   = agentstate.NeedsInput
	AgentStateIdle         = agentstate.Idle
	AgentStateDone         = agentstate.Done
	AgentStateErrored      = agentstate.Errored
	AgentStateUnknown      = agentstate.Unknown
	AgentStateLimitReached = agentstate.LimitReached
)

// AgentStateNames lists the accepted wire values in a stable order, for the
// verb's accepted-value schema and for input validation.
var AgentStateNames = agentstate.Names()

// ParseAgentState resolves a wire value to an AgentState. See agentstate.Parse.
func ParseAgentState(s string) (AgentState, bool) { return agentstate.Parse(s) }

// AgentReport is one source's claim on a window's agent state. Source empty
// means AgentSourceReport, so the zero value is an explicit report, which is
// what every caller predating sources sends.
type AgentReport struct {
	State   AgentState
	Message string
	Source  AgentSource
	Harness string // optional harness id, reported back by get-agent-state
	// Kind is what sort of block a needs_input report is: harness.PromptKindApproval
	// or harness.PromptKindQuestion. The screen, title and notify tiers take it
	// from the rule that matched, and a hook reporter sends it as the kind param
	// of set-agent-state. A report that names none is guessed from its message;
	// see agentKindOf. It means nothing for any other state.
	Kind string
	// SessionID is the harness's own id for the conversation the report is
	// about. It is set by hook reporters, and setting it turns on the nested
	// and foreign session guard in sessionGuard. A report without one is
	// treated exactly as reports were before the field existed.
	SessionID string
	// HarnessPID is the pid of the harness process that ran the hook, as the
	// hook worked it out, and 0 when it did not say. It only matters with a
	// SessionID: sessionGuard lets a different session id from the same
	// harness process take the pane over, since that is a new conversation in
	// the same program (/clear, /resume) and not a nested run.
	HarnessPID int
	// HarnessIsCaller is set by the daemon when HarnessPID is the process
	// that sent the report or one of its ancestors (callerRunsHarness). Only
	// then is the pid known to name a process of this machine, and only then
	// may its exit clear the pane. See harnessProc.
	HarnessIsCaller bool
	harnessGone     bool
	// SessionStart marks the report of a conversation starting or resuming,
	// as the hook's SessionStart sends it. It only matters with SharedServer.
	SessionStart bool
	// SharedServer is set by the daemon, from the manifest of the harness the
	// report names, for a harness that runs every conversation's hooks from
	// one server process (harness.Manifest.SharedServer). sessionGuard then
	// lets a different conversation take the pane over only on SessionStart.
	SharedServer bool
	// IfState, when not empty, applies the report only if the window's state
	// is one of these right now. It is how a hook says "clear the block once
	// the tool ran" without also turning a finished pane back to working.
	IfState []AgentState
	// Subagent is the subagent a needs_input report waits on, empty when the
	// prompt is the pane's own agent's. A harness that runs subagents shows
	// their prompts as its own, and only the subagent's next event says the
	// prompt was answered.
	Subagent string
	// IfSubagent, when not empty, applies the report only if the window is on
	// needs_input and waits on that subagent's prompt. It is how a subagent's hook clears the prompt
	// it raised without clearing one the main agent or another subagent
	// raised meanwhile.
	IfSubagent  string
	foreignPane bool
	fromPane    bool
	paneWroteAt int64
	event       bool
	promptGone  bool
}

// SetDaemonWindowAgentState records an explicit report on the window matching
// target. It is the no-source path: every caller that names no source is
// reporting for itself and gets AgentSourceReport, the highest rank, which is
// exactly the authority such a caller had before sources existed.
func (s *Session) SetDaemonWindowAgentState(target string, state AgentState, message string) error {
	_, _, err := s.ApplyAgentReport(target, AgentReport{State: state, Message: message})
	return err
}

// ApplyAgentReport records r on the window matching target, stamping the time it
// was set, and returns the window's effective state afterwards and whether r was
// the thing that set it. It runs through mutateState, so an applied report bumps
// the session version and reaches attached clients through the same state-push
// every other daemon-side mutation uses.
//
// A report from a source ranked below the one that currently owns the window is
// refused, and refusing is not an error: a screen rule guessing at a pane whose
// harness reports for itself is the ordinary case, and the weaker guess has to
// leave the better answer alone. A refused report changes nothing, so it neither
// bumps the version nor pushes. The one exception is blockerOverridesClaim.
//
// The output-stall heuristic is deliberately not routed through here; see
// applyStallHeuristic for why.
func (s *Session) ApplyAgentReport(target string, r AgentReport) (AgentState, bool, error) {
	effective, applied, _, err := s.applyAgentReport(target, r)
	return effective, applied, err
}

const (
	agentRefusedOutranked      = "outranked"
	agentRefusedIfState        = "if_state"
	agentRefusedForeignSession = "foreign_session"
	agentRefusedForeignHarness = "foreign_harness"
	agentRefusedForeignPane    = "foreign_pane"
)

type sharedGuard struct {
	harness, owner string
	history        []string
	armedAt        int64
}

const sharedGuardHistory = 4

func (g sharedGuard) accepts(id string) bool {
	return id == g.owner || slices.Contains(g.history, id)
}

func (s *Session) noteSharedGuard(windowID string, r AgentReport) {
	if !r.SharedServer || r.SessionID == "" {
		return
	}
	g, armed := s.agentSharedGuards[windowID]
	switch {
	case r.State == AgentStateNone:
		delete(s.agentSharedGuards, windowID)
		return
	case !armed || g.owner == "" || g.harness != r.Harness:
		if !r.SessionStart {
			return
		}
		g = sharedGuard{harness: r.Harness, owner: r.SessionID, armedAt: time.Now().UnixNano()}
	case r.SessionID == g.owner:
		return
	default:
		next := []string{g.owner}
		for _, id := range g.history {
			if id != r.SessionID && len(next) < sharedGuardHistory {
				next = append(next, id)
			}
		}
		g.owner, g.history = r.SessionID, next
	}
	if s.agentSharedGuards == nil {
		s.agentSharedGuards = make(map[string]sharedGuard)
	}
	s.agentSharedGuards[windowID] = g
}

func refusedAsForeign(reason string) bool {
	return reason == agentRefusedForeignSession || reason == agentRefusedForeignHarness || reason == agentRefusedForeignPane
}

type errAgentReportRefused string

func (e errAgentReportRefused) Error() string { return "agent report refused: " + string(e) }

func sessionGuard(w *WindowState, claim agentClaim, held bool, ownerPID int, g sharedGuard, r AgentReport) string {
	if r.SessionID == "" {
		return ""
	}
	if r.SharedServer && g.owner != "" && g.harness == r.Harness && r.SessionID != g.owner {
		if g.accepts(r.SessionID) {
			return ""
		}
		if !r.SessionStart {
			return agentRefusedForeignSession
		}
	}
	if !held || claim.source != AgentSourceReport {
		return ""
	}
	if w.AgentState != AgentStateWorking && w.AgentState != AgentStateNeedsInput {
		return ""
	}
	if w.AgentSessionID != "" && w.AgentSessionID != r.SessionID {
		if r.HarnessPID > 1 && r.HarnessPID == ownerPID {
			return ""
		}
		return agentRefusedForeignSession
	}
	if r.Harness != "" && claim.identity == identityReport && claim.harness != "" && claim.harness != r.Harness {
		return agentRefusedForeignHarness
	}
	return ""
}

func (s *Session) agentReportGuard(target string, r AgentReport) (string, string) {
	id, _, reason, _ := s.guardReport(target, r, false)
	return id, reason
}

func (s *Session) guardReport(target string, r AgentReport, strict bool) (string, AgentState, string, error) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	idx, err := findWindowStateIndex(s.state.Windows, target)
	if err != nil {
		return "", AgentStateNone, "", err
	}
	w := &s.state.Windows[idx]
	if r.foreignPane {
		return w.ID, w.AgentState, agentRefusedForeignPane, nil
	}
	claim, held := s.agentClaims[w.ID]
	reason := sessionGuard(w, claim, held, s.agentHarnessPIDs[w.ID].pid, s.agentSharedGuards[w.ID], r)
	switch {
	case reason != "" || !strict:
	case r.SessionID != "" && w.AgentSessionID != "" && r.SessionID != w.AgentSessionID && !sharedAccepts(s.agentSharedGuards[w.ID], r):
		reason = agentRefusedForeignSession
	case r.Harness != "" && w.AgentHarness != "" && r.Harness != w.AgentHarness:
		reason = agentRefusedForeignHarness
	}
	return w.ID, w.AgentState, reason, nil
}

func sharedAccepts(g sharedGuard, r AgentReport) bool {
	return r.SharedServer && g.owner != "" && g.harness == r.Harness && g.accepts(r.SessionID)
}

func (s *Session) activityReportGuard(target string, r AgentReport) (string, AgentState, string, error) {
	return s.guardReport(target, r, true)
}

func (s *Session) applyAgentReport(target string, r AgentReport) (AgentState, bool, string, error) {
	if r.Source == "" {
		r.Source = AgentSourceReport
	}
	var effective AgentState
	applied := false
	var holdAt int64
	var heldWindow, heldPTY string
	heardOnly := false
	err := s.mutateState(func(st *SessionState) error {
		idx, err := findWindowStateIndex(st.Windows, target)
		if err != nil {
			return err
		}
		w := &st.Windows[idx]
		if ask, asking := s.askShowLocked(w); asking {
			shown := w
			beneath := *shown
			ask.beneath.put(&beneath)
			w = &beneath
			defer func() {
				if s.settleAskShowLocked(shown, &beneath, ask, applied) {
					effective = AgentStateNeedsInput
				}
			}()
		}
		if r.foreignPane {
			effective = w.AgentState
			return errAgentReportRefused(agentRefusedForeignPane)
		}
		claim, held := s.agentClaims[w.ID]
		if lookRepeatsClaim(claim, held, w, r) {
			effective = w.AgentState
			return errAgentLookUnchanged
		}
		prev := w.AgentState
		if len(r.IfState) > 0 && !slices.Contains(r.IfState, prev) {
			effective = prev
			if s.noteHeardLocked(w, claim, held, r) {
				heardOnly = true
				return nil
			}
			return errAgentReportRefused(agentRefusedIfState)
		}
		if r.IfSubagent != "" && (prev != AgentStateNeedsInput || !held || claim.subagent != r.IfSubagent) {
			effective = prev
			return errAgentReportRefused(agentRefusedIfState)
		}
		if reason := sessionGuard(w, claim, held, s.agentHarnessPIDs[w.ID].pid, s.agentSharedGuards[w.ID], r); reason != "" {
			effective = prev
			return errAgentReportRefused(reason)
		}
		if hookPromptHolds(claim, held, w, r) {
			effective = w.AgentState
			return errAgentClaimHeld
		}
		override := false
		if held && r.Source.Rank() < claim.source.Rank() && !eventClaimStale(claim, w, r) {
			if !blockerOverridesClaim(w, r, claim, time.Now()) {
				effective = w.AgentState
				if screenNamesPrompt(claim, w, r) {
					w.AgentMessage = ClampDisplayText(r.Message)
					return nil
				}
				return errAgentClaimHeld
			}
			override = true
		}
		next := agentClaim{source: r.Source, harness: harnessAfterReport(w, r), auto: claim.auto, misses: claim.misses, event: r.event}
		next.screenWins = r.Source == AgentSourceTranscript && r.State == AgentStateWorking
		next.sawProcess = claim.sawProcess && r.State != AgentStateNone
		next.identity = identityAfterReport(claim, r, next.harness)
		if r.State == AgentStateNeedsInput {
			next.subagent = r.Subagent
		}
		switch {
		case override:
			next.blocker = true
			next.prior = priorOf(claim, w)
		case claim.blocker && r.Source == claim.source && r.State == prev:
			next.blocker, next.prior = true, claim.prior
		case r.Source == AgentSourceScreen && agentStateBlocks(r.State) && held:
			next.blocker = true
			next.prior = priorOf(claim, w)
		}
		switch {
		case r.State == AgentStateNeedsInput, r.fromPane, r.paneWroteAt != 0:
		case prev == AgentStateNeedsInput:
			next.promptHold = time.Now().UnixNano()
			holdAt = next.promptHold
		default:
			next.promptHold, next.holdFg = claim.promptHold, claim.holdFg
		}
		if next.blocker {
			next.herdrAt, next.herdrAnchors = claim.herdrAt, claim.herdrAnchors
		}
		w.AgentState = r.State
		w.AgentMessage = ClampDisplayText(r.Message)
		w.AgentKind = agentKindOf(r)
		w.AgentHarness = next.harness
		w.AgentStateAt = time.Now().UnixNano()
		s.noteSharedGuard(w.ID, r)
		if r.SessionID != "" {
			w.AgentSessionID = r.SessionID
			w.AgentSessionHarness = r.Harness
			if w.AgentSessionHarness == "" {
				w.AgentSessionHarness = next.harness
			}
			s.setAgentHarnessPID(w.ID, r.HarnessPID, r.HarnessIsCaller, r.harnessGone)
		}
		if r.State == AgentStateNone {
			w.AgentMeta = nil
			delete(s.agentHarnessPIDs, w.ID)
		}
		s.setAgentClaim(w.ID, next)
		heldWindow, heldPTY = w.ID, w.PTYID
		effective = r.State
		applied = true
		return nil
	})
	if holdAt != 0 {
		s.notePromptHoldFg(heldWindow, heldPTY, holdAt)
	}
	if err == nil && heardOnly {
		return effective, false, agentRefusedIfState, nil
	}
	if errors.Is(err, errAgentClaimHeld) {
		return effective, false, agentRefusedOutranked, nil
	}
	if errors.Is(err, errAgentLookUnchanged) {
		return effective, false, "", nil
	}
	if refused, ok := errors.AsType[errAgentReportRefused](err); ok {
		return effective, false, string(refused), nil
	}
	if err != nil {
		return effective, false, "", err
	}
	return effective, applied, "", nil
}

func harnessAfterReport(w *WindowState, r AgentReport) string {
	if r.State == AgentStateNone {
		return ""
	}
	if r.Harness != "" {
		return r.Harness
	}
	return w.AgentHarness
}

func agentKindOf(r AgentReport) string {
	if r.State != AgentStateNeedsInput {
		return ""
	}
	if r.Kind != "" || r.Source == AgentSourceProgram {
		return r.Kind
	}
	return harness.GuessPromptKind(r.Message)
}

func agentBlockedBy(w WindowState) string {
	if w.AgentState != AgentStateNeedsInput {
		return ""
	}
	return w.AgentKind
}

func identityAfterReport(claim agentClaim, r AgentReport, harness string) identityTier {
	switch {
	case harness == "":
		return ""
	case r.Harness != "" && r.Source == AgentSourceReport:
		return identityReport
	default:
		return claim.identity
	}
}

func screenNamesPrompt(claim agentClaim, w *WindowState, r AgentReport) bool {
	return claim.screenWins && r.Source == AgentSourceScreen && r.paneWroteAt != 0 &&
		r.State == w.AgentState && agentStateBlocks(r.State) && r.Message != "" &&
		w.AgentMessage == herdrBlockedPlaceholder
}

func priorOf(claim agentClaim, w *WindowState) agentPriorClaim {
	return agentPriorClaim{
		source:       claim.source,
		state:        w.AgentState,
		harness:      w.AgentHarness,
		herdrAt:      claim.herdrAt,
		herdrAnchors: claim.herdrAnchors,
		screenWins:   claim.screenWins,
		message:      w.AgentMessage,
		kind:         w.AgentKind,
		event:        claim.event,
	}
}

const agentBlockerOverrideGrace = 2 * time.Second

func agentStateBlocks(state AgentState) bool {
	return state == AgentStateNeedsInput
}

func (s *Session) paneForeground(ptyID string) (int, bool) {
	pty := s.GetPTY(ptyID)
	if pty == nil || pty.IsExited() {
		return 0, false
	}
	info, ok := foregroundProcess(pty.ShellPID())
	return info.pid, ok
}

func (s *Session) notePromptHoldFg(windowID, ptyID string, holdAt int64) {
	fg, ok := s.paneForeground(ptyID)
	if !ok {
		return
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if c, held := s.agentClaims[windowID]; held && c.promptHold == holdAt {
		c.holdFg = fg
		s.agentClaims[windowID] = c
	}
}

func (s *Session) promptStillShown(w WindowState) bool {
	s.stateMu.RLock()
	c := s.agentClaims[w.ID]
	s.stateMu.RUnlock()
	if c.promptHold == 0 {
		return false
	}
	pty := s.GetPTY(w.PTYID)
	if pty == nil || pty.IsExited() {
		return false
	}
	if c.holdFg == 0 {
		return true
	}
	fg, ok := s.paneForeground(w.PTYID)
	return !ok || fg == c.holdFg
}

func blockerOverridesClaim(w *WindowState, r AgentReport, claim agentClaim, now time.Time) bool {
	if r.Source == AgentSourceScreen && r.State == AgentStateLimitReached && w.AgentState != r.State {
		return r.paneWroteAt > w.AgentStateAt && agentStateRests(w.AgentState)
	}
	if r.Source != AgentSourceScreen || !agentStateBlocks(r.State) || w.AgentState == r.State {
		return false
	}
	if claim.screenWins && r.paneWroteAt != 0 {
		return true
	}
	if r.paneWroteAt <= w.AgentStateAt {
		return false
	}
	if agentStateRests(w.AgentState) {
		return true
	}
	return now.UnixNano()-w.AgentStateAt >= int64(agentBlockerOverrideGrace)
}

func agentStateRests(s AgentState) bool {
	return s == AgentStateIdle || s == AgentStateUnknown || s == AgentStateDone
}

func eventClaimStale(claim agentClaim, w *WindowState, r AgentReport) bool {
	return claim.event && r.paneWroteAt > w.AgentStateAt && r.Source.Rank() >= AgentSourceScreen.Rank()
}

func hookPromptHolds(claim agentClaim, held bool, w *WindowState, r AgentReport) bool {
	if !held || w.AgentState != AgentStateNeedsInput || !claim.hookEventPrompt() {
		return false
	}
	if r.Source.Rank() >= AgentSourceReport.Rank() || r.promptGone || r.State == AgentStateNone {
		return false
	}
	return r.Source != AgentSourceScreen || !agentStateBlocks(r.State) || r.paneWroteAt == 0
}

func (s *Session) releaseAgentBlockerOverride(windowID string) bool {
	released := false
	_ = s.mutateState(func(st *SessionState) error {
		idx, err := findWindowStateIndex(st.Windows, windowID)
		if err != nil {
			return err
		}
		w := &st.Windows[idx]
		claim, held := s.agentClaims[w.ID]
		if !held || !claim.blocker {
			return errNoBlockerRelease
		}
		w.AgentState = claim.prior.state
		w.AgentMessage, w.AgentKind = claim.prior.message, claim.prior.kind
		w.AgentHarness = claim.prior.harness
		w.AgentStateAt = time.Now().UnixNano()
		s.setAgentClaim(w.ID, agentClaim{
			source:       claim.prior.source,
			harness:      claim.prior.harness,
			auto:         claim.auto,
			sawProcess:   claim.sawProcess,
			herdrAt:      claim.prior.herdrAt,
			herdrAnchors: claim.prior.herdrAnchors,
			screenWins:   claim.prior.screenWins,
			event:        claim.prior.event,
		})
		released = true
		return nil
	})
	return released
}

var errNoBlockerRelease = blockerNoRelease{}

type blockerNoRelease struct{}

func (blockerNoRelease) Error() string { return "no visible-blocker override to release" }

var errAgentClaimHeld = agentClaimHeld{}

type agentClaimHeld struct{}

func (agentClaimHeld) Error() string { return "agent state is held by a higher-ranked source" }

var errAgentLookUnchanged = agentLookUnchanged{}

type agentLookUnchanged struct{}

func (agentLookUnchanged) Error() string { return "agent state is unchanged" }

func lookRepeatsClaim(claim agentClaim, held bool, w *WindowState, r AgentReport) bool {
	return held && r.paneWroteAt != 0 && !r.event && !claim.event &&
		claim.source == r.Source &&
		w.AgentState == r.State &&
		w.AgentMessage == r.Message &&
		w.AgentKind == agentKindOf(r) &&
		harnessAfterReport(w, r) == w.AgentHarness
}

func (s *Session) applyStallHeuristic(now time.Time, stall time.Duration, lastOutput func(ptyID string) int64, look func(ptyID string) bool) int {
	if stall <= 0 {
		return 0
	}
	cutoff := now.Add(-stall).UnixNano()

	type candidate struct{ windowID, ptyID string }
	var pending []candidate
	s.stateMu.RLock()
	for i := range s.state.Windows {
		w := &s.state.Windows[i]
		if w.AgentState == AgentStateWorking && stalledAt(w.AgentStateAt, w.PTYID, cutoff, lastOutput) {
			pending = append(pending, candidate{w.ID, w.PTYID})
		}
	}
	s.stateMu.RUnlock()
	if len(pending) == 0 {
		return 0
	}

	quiet := AgentStateIdle
	if look != nil {
		quiet = AgentStateUnknown
	}

	if look != nil {
		kept := pending[:0]
		for _, c := range pending {
			if c.ptyID != "" && look(c.ptyID) {
				continue
			}
			kept = append(kept, c)
		}
		pending = kept
	}

	flipped := 0
	_ = s.mutateState(func(st *SessionState) error {
		for _, c := range pending {
			idx, err := findWindowStateIndex(st.Windows, c.windowID)
			if err != nil {
				continue
			}
			w := &st.Windows[idx]
			if w.AgentState != AgentStateWorking || !stalledAt(w.AgentStateAt, w.PTYID, cutoff, lastOutput) {
				continue
			}
			workEnd := w.AgentStateAt
			if w.PTYID != "" {
				workEnd = max(workEnd, lastOutput(w.PTYID))
			}
			s.noteWorkEndLocked(w.ID, workEnd)
			w.AgentState = quiet
			w.AgentStateAt = now.UnixNano()
			claim := s.agentClaims[w.ID]
			claim.source = AgentSourceStall
			s.setAgentClaim(w.ID, claim)
			flipped++
		}
		if flipped == 0 {
			return errNoStallChange
		}
		return nil
	})
	return flipped
}

func stalledAt(stateAt int64, ptyID string, cutoff int64, lastOutput func(ptyID string) int64) bool {
	if ptyID != "" {
		if out := lastOutput(ptyID); out > stateAt {
			stateAt = out
		}
	}
	return stateAt <= cutoff
}

var errNoStallChange = stallNoChange{}

type stallNoChange struct{}

func (stallNoChange) Error() string { return "no agent-state change" }
