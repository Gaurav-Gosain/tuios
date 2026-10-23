package session

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// A question shown as the asking pane's agent state.
//
// An agent blocked on ask-human is waiting on the person, which is what
// needs_input means, and the rail, the cockpit, list-agents and wait-for
// agent-state all read the state. So while an ask-human call from inside a
// pane waits, the pane shows needs_input, kind question, with the question as
// its note, and when the call returns (answered, dismissed, replaced, timed
// out) the pane shows what it showed before. The agent does not have to
// report it, and it cannot forget to report its way back.
//
// The question is laid over the pane's state rather than reported, so the
// claim on the pane (agentClaims) is not touched:
//
//   - A report that arrives while the question shows is applied to the state
//     beneath it (applyAgentReport), and the pane shows that one when the
//     question ends. The question never overwrites a newer report: a pane
//     that said idle while it waited ends on idle. A report of needs_input,
//     errored or none is something the person has to see now, so it ends the
//     question's showing at once.
//   - A report that moves the pane off the question is judged against the
//     state beneath, which is never needs_input, so it never takes the #1054
//     prompt hold (promptStillShown). Nor does the question ending: it goes
//     through no report at all. Typing into the pane after the question is
//     held exactly as it was before it.
//   - Any other writer of the state (the detector seeing the agent exit, the
//     agent features turned off) stamps a new AgentStateAt, and the question
//     then no longer shows: the end leaves that state alone.
//   - The Inbox shows the ask item, not a question item beside it
//     (noteAgentState skips the needs_input the question shows).
//
// A pane already on needs_input when it asks is left as it is. That state is
// a prompt on its screen, or the agent's own report of one, and it stays true
// whatever the person answers to the question. Laying the question over it
// and handing it back at the end would at best repeat it, and at worst stamp
// it as new, so the detector and the Inbox would take an old prompt for a
// fresh one. The ask item still shows the question.
//
// A question from outside every pane, or one a script outside names a pane
// for, shows nothing on any pane: no agent in a pane is blocked on it.

// askShow is the question a pane shows as its state, and what it shows
// beneath.
type askShow struct {
	requestID string
	// at is the AgentStateAt the question was shown with. A state stamped
	// later was written by something else.
	at      int64
	beneath agentShown
}

// agentShown is the part of a window's state the question replaces.
type agentShown struct {
	state   AgentState
	message string
	kind    string
	at      int64
}

func shownOf(w *WindowState) agentShown {
	return agentShown{state: w.AgentState, message: w.AgentMessage, kind: w.AgentKind, at: w.AgentStateAt}
}

func (a agentShown) put(w *WindowState) {
	w.AgentState, w.AgentMessage, w.AgentKind, w.AgentStateAt = a.state, a.message, a.kind, a.at
}

// askShadows reports whether a report of state, made while a question shows,
// goes beneath it. needs_input, errored and none are for the person to see
// now.
func askShadows(state AgentState) bool {
	switch state {
	case AgentStateWorking, AgentStateIdle, AgentStateDone, AgentStateUnknown:
		return true
	}
	return false
}

// askShowLocked is the question w shows, if it still shows one. It forgets a
// question something else has written over. The caller holds stateMu for
// writing.
func (s *Session) askShowLocked(w *WindowState) (askShow, bool) {
	a, ok := s.askShows[w.ID]
	if !ok {
		return askShow{}, false
	}
	if w.AgentState != AgentStateNeedsInput || w.AgentStateAt != a.at {
		delete(s.askShows, w.ID)
		return askShow{}, false
	}
	return a, true
}

// settleAskShowLocked ends a report made while shown showed a question.
// beneath is the window as the report left it, with the state beneath the
// question. applied says the report set the state. It returns whether the
// question still shows.
func (s *Session) settleAskShowLocked(shown, beneath *WindowState, a askShow, applied bool) bool {
	if applied && !askShadows(beneath.AgentState) {
		delete(s.askShows, shown.ID)
		*shown = *beneath
		return false
	}
	a.beneath = shownOf(beneath)
	s.askShows[shown.ID] = a
	over := shownOf(shown)
	*shown = *beneath
	over.put(shown)
	return true
}

// askShownLocked reports whether a needs_input with message on the pane
// window is the pane showing one of its own open questions. The caller holds
// a.mu.
func (a *attentionStore) askShownLocked(sessionName, window, message string) bool {
	if window == "" {
		return false
	}
	for _, h := range a.asks {
		if h.session == sessionName && h.window == window && ClampDisplayText(h.question) == message {
			return true
		}
	}
	return false
}

// errNoAskShow tells mutateState that showing or ending a question changed
// nothing. It never leaves the package.
var errNoAskShow = askShowNoChange{}

type askShowNoChange struct{}

func (askShowNoChange) Error() string { return "no question to show or end" }

// showAsk shows question as the agent state of the pane windowID while an
// ask-human call from it waits. A pane on needs_input is left alone; see the
// file comment. A question that replaces one the pane shows keeps what was
// beneath that one.
func (s *Session) showAsk(windowID, requestID, question string) {
	_ = s.mutateState(func(st *SessionState) error {
		idx, err := findWindowStateIndex(st.Windows, windowID)
		if err != nil {
			return err
		}
		w := &st.Windows[idx]
		cur, showing := s.askShowLocked(w)
		beneath := cur.beneath
		if !showing {
			if w.AgentState == AgentStateNeedsInput {
				return errNoAskShow
			}
			beneath = shownOf(w)
		}
		w.AgentState = AgentStateNeedsInput
		w.AgentMessage = ClampDisplayText(question)
		w.AgentKind = harness.PromptKindQuestion
		w.AgentStateAt = time.Now().UnixNano()
		if s.askShows == nil {
			s.askShows = make(map[string]askShow)
		}
		s.askShows[w.ID] = askShow{requestID: requestID, at: w.AgentStateAt, beneath: beneath}
		return nil
	})
}

// endAskShow puts back the state beneath the question requestID showed on
// the pane windowID, when the pane still shows it. The state is stamped now:
// the agent comes back to it now, and a working state stamped before a long
// wait would read as silent to the stall timer at once.
func (s *Session) endAskShow(windowID, requestID string) {
	_ = s.mutateState(func(st *SessionState) error {
		a, ok := s.askShows[windowID]
		if !ok || a.requestID != requestID {
			return errNoAskShow
		}
		idx, err := findWindowStateIndex(st.Windows, windowID)
		if err != nil {
			delete(s.askShows, windowID)
			return err
		}
		w := &st.Windows[idx]
		if _, showing := s.askShowLocked(w); !showing {
			return errNoAskShow
		}
		delete(s.askShows, windowID)
		a.beneath.put(w)
		w.AgentStateAt = time.Now().UnixNano()
		return nil
	})
}
