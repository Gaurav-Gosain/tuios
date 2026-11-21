package session

import (
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

type childAskTable struct {
	mu     sync.Mutex
	mailed map[string]int64
}

func (t *childAskTable) first(window string, stamp int64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.mailed == nil {
		t.mailed = make(map[string]int64)
	}
	if t.mailed[window] == stamp {
		return false
	}
	t.mailed[window] = stamp
	return true
}

func (t *childAskTable) forget(window string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.mailed, window)
}

func (d *Daemon) noteChildQuestionEvent(ev SessionEvent) {
	if ev.Type == EventWindowClosed {
		d.childAsks.forget(ev.Window)
		return
	}
	if ev.Type != EventAgentState || !ev.link.linked || ev.link.delegated || ev.State != AgentStateNeedsInput.Name() {
		return
	}
	go d.childQuestionLook(ev.Window)
}

func (d *Daemon) childQuestionLook(window string) {
	sess := d.sessionHoldingWindow(window)
	if sess == nil {
		return
	}
	w, ok := findWindowState(sess.GetState(), window)
	if !ok || w.AgentLink == nil || w.StartedBy == nil || w.Delegation != nil || w.AgentState != AgentStateNeedsInput {
		return
	}
	if AgentKindReachesPerson(w.AgentKind) || !d.attention.parentFirst.Load() {
		return
	}
	if !d.childAsks.first(window, w.AgentStateAt) {
		return
	}
	q := d.delegateQuestionOf(sess, w)
	name := windowLabelOf(w)
	if name == "" {
		name = shortWindowID(w.ID)
	}
	body := strings.Join(q.Lines, "\n") + "\n\n" +
		"[tuios: answer it with tuios respond -s " + sess.Name() + " -w " + w.ID + " choose N, after tuios peek-prompt shows the choices. " +
		"The person is asked if it still waits in a while. An approval is never yours to answer.]"
	if d.mailParent(*w.StartedBy, "", sess, w, "Your agent "+name+" asks a question", body, "", "") == 0 {
		d.attention.releaseParentHold(sess.Name(), window)
	}
}

func parentMayRespond(window string, target WindowState) bool {
	return window != "" && target.AgentLink != nil && target.StartedBy != nil &&
		target.StartedBy.Window == window && agentBlockedBy(target) == harness.PromptKindQuestion
}
