package session

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

type askShow struct {
	requestID string
	at        int64
	beneath   agentShown
}

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

func askShadows(state AgentState) bool {
	switch state {
	case AgentStateWorking, AgentStateIdle, AgentStateDone, AgentStateUnknown:
		return true
	}
	return false
}

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

var errNoAskShow = askShowNoChange{}

type askShowNoChange struct{}

func (askShowNoChange) Error() string { return "no question to show or end" }

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
